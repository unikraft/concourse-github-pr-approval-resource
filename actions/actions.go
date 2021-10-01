// SPDX-License-Identifier: BSD-3-Clause
//
// Authors: Alexander Jung <alex@nderjung.net>
//
// Copyright (c) 2020, Alexander Jung.  All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions
// are met:
//
// 1. Redistributions of source code must retain the above copyright
//    notice, this list of conditions and the following disclaimer.
// 2. Redistributions in binary form must reproduce the above copyright
//    notice, this list of conditions and the following disclaimer in the
//    documentation and/or other materials provided with the distribution.
// 3. Neither the name of the copyright holder nor the names of its
//    contributors may be used to endorse or promote products derived from
//    this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
// ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
// LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
// CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
// SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
// INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
// CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
// ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.
package actions

import (
  "os"
  "fmt"
  "log"
  "regexp"
  "strings"
  "reflect"
  "encoding/json"

  "github.com/google/go-github/v32/github"

  "github.com/unikraft/concourse-github-pr-approval-resource/api"
)

// Source parameters provided by the resource.
type Source struct {
  // Meta
  SkipSSLVerification    bool   `json:"skip_ssl"`
  GithubEndpoint         string `json:"github_endpoint"`

  // The repository to interface with
  Repository             string `json:"repository"`
  DisableGitLfs          bool   `json:"disable_git_lfs"`

  // Access methods
  AccessToken            string `json:"access_token"`
  Username               string `json:"username"`
  Password               string `json:"password"`

  // Selection criteria
  OnlyMergeable          bool   `json:"only_mergeable"`
  States               []string `json:"states"`
  Labels               []string `json:"labels"`
  
  MinApprovals           int    `json:"min_approvals"`
  ApproverComments     []string `json:"approver_comments"`
  ApproverTeams        []string `json:"approver_teams"`
  MinReviews             int    `json:"min_reviews"`
  ReviewerComments     []string `json:"reviewer_comments"`
  ReviewerTeams        []string `json:"reviewer_teams"`
  ReviewStates         []string `json:"review_states"`

  IgnoreStates         []string `json:"ignore_states"`
  IgnoreLabels         []string `json:"ignore_labels"`
}

type Response struct {
  ReviewID  string `json:"review_id"`
  CommentID string `json:"comment_id"`
  CreatedAt string `json:"created_at"`
}

// Version communicated with Concourse.
type Version struct {
  PrID         string   `json:"pr_id"`
  ApprovedBy   string    `json:"approved_by"`
  approvedBy []*Response
  ReviewedBy   string    `json:"reviewed_by"`
  reviewedBy []*Response
  lastUpdated  int64
}

// Metadata has a key name and value
type MetadataField struct {
  Name  string `json:"name"`
  Value string `json:"value"`
}

// Metadata contains the serialized interface
type Metadata []*MetadataField

// Add a MetadataField to the Metadata struct
func (m *Metadata) Add(name, value string) {
  *m = append(*m, &MetadataField{
    Name: name,
    Value: value,
  })
}

// Get a MetadataField value from the Metadata struct
func (m *Metadata) Get(name string) (string, error) {
  for _, i := range *m {
    if name == i.Name {
      return i.Value, nil
    }
  }

  return "", fmt.Errorf("metadata index does not exist: %s", name)
}

func serializeStruct(meta interface{}) Metadata {
  var res Metadata
  v := reflect.ValueOf(meta)
  typeOfS := v.Type()

  for i := 0; i< v.NumField(); i++ {
    res.Add(
      typeOfS.Field(i).Tag.Get("json"),
      fmt.Sprintf("%v", v.Field(i).Interface()),
    )
  }

  return res
}

// requestsState checks whether the source requests this particular state
func (source *Source) requestsState(state string) bool {
  ret := false

  // if there are no set states, assume only "open" states
  if len(source.States) == 0 {
    ret = state == "open"
  } else {
    for _, s := range source.States {
      if s == state {
        ret = true
        break
      }
    }
  }

  // Ensure ignored states
  for _, s := range source.IgnoreStates {
    if s == state {
      ret = false
      break
    }
  }

  return ret
}

// requestsReviewState checks whether the PR review matches the desired state
func (source *Source) requestsReviewState(state string) bool {
  state = strings.ToLower(state)
  for _, s := range source.ReviewStates {
    if state == strings.ToLower(s) {
      return true
    }
  }

  return false
}

// requestsLabels checks whether the source requests these set of labels
func (source *Source) requestsLabels(labels []*github.Label) bool {
  ret := false

  // If no set labels, assume all
  if len(source.Labels) == 0 {
    ret = true
  } else {
    includeLoop:
    for _, rl := range source.Labels {
      for _, rr := range labels {
        if rl == rr.GetName() {
          ret = true
          break includeLoop
        }
      }
    }
  }

  excludeLoop:
  for _, rl := range source.IgnoreLabels {
    for _, rr := range labels {
      if rl == rr.GetName() {
        ret = false
        break excludeLoop
      }
    }
  }

  return ret
}

// requestsReviewerRegex determines if the source requests this reviewer regex
func (source *Source) requestsReviewerRegex(comment string) bool {
  ret := false

  if len(source.ReviewerComments) == 0 {
    ret = true
  } else {
    for _, c := range source.ReviewerComments {
      matched, _ := regexp.Match(c, []byte(comment))
      if matched {
        ret = true
      }
    }
  }

  return ret
}

// requestsReviewerTeam determines if the source requests this reviewer team
func (source *Source) requestsReviewerTeam(c *api.GithubClient, username string) bool {
  // No team set, we can assume this is "catch all"
  if len(source.ReviewerTeams) == 0 {
    return true
  }

  for _, t := range source.ReviewerTeams {
    if ok, _ := c.UserMemberOfTeam(username, t); ok {
      return true
    }
  }

  return false
}

// hasMinReviewers determines whether the supplied list meets the requested
// minimum
func (source *Source) hasMinReviewers(numReviewers int) bool {
  min := 1
  if source.MinReviews > min {
    min = source.MinReviews
  }

  return numReviewers >= min
}

// requestsApproverRegex determines if the source requests this approver regex
func (source *Source) requestsApproverRegex(comment string) bool {
  ret := false

  if len(source.ApproverComments) == 0 {
    ret = true
  } else {
    for _, c := range source.ApproverComments {
      matched, _ := regexp.Match(c, []byte(comment))
      if matched {
        ret = true
      }
    }
  }

  return ret
}

// requestsApproverTeam determines if the source requests this approver team
func (source *Source) requestsApproverTeam(c *api.GithubClient, username string) bool {
  // No team set, we can assume this is "catch all"
  if len(source.ApproverTeams) == 0 {
    return true
  }

  for _, t := range source.ApproverTeams {
    if ok, _ := c.UserMemberOfTeam(username, t); ok {
      return true
    }
  }

  return false
}

// hasMinApprovers determines whether the supplied list meets the requested
// minimum
func (source *Source) hasMinApprovers(numApprovers int) bool {
  min := 1
  if source.MinApprovals > min {
    min = source.MinApprovals
  }

  return numApprovers >= min
}

var logger = log.New(os.Stderr, "resource:", log.Lshortfile)

// doOutput ...
func doOutput(output interface{}, encoder *json.Encoder, logger *log.Logger) error {
  _, err := json.MarshalIndent(output, "", "  ")
  if err != nil {
    return err
  }

  // encode output to stdout
  return encoder.Encode(output)
}
