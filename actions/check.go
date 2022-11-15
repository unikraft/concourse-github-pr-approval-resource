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
  "sort"
  "strconv"
  "encoding/json"

  "github.com/spf13/cobra"
  "github.com/unikraft/concourse-github-pr-approval-resource/api"
)

// CheckCmd ...
var CheckCmd = &cobra.Command{
  Use:                   "check",
  Short:                 "Run the check step",
  Run:                   doCheckCmd,
  DisableFlagsInUseLine: true,
}

// CheckRequest from the check stdin.
type CheckRequest struct {
  Source  Source  `json:"source"`
  Version Version `json:"version"`
}

// CheckResponse represents the structure Concourse expects on stdout
type CheckResponse []Version

func doCheckCmd(cmd *cobra.Command, args []string) {
  decoder := json.NewDecoder(os.Stdin)
  decoder.DisallowUnknownFields()

  // Concourse passes .json on stdin
  var req CheckRequest
  if err := decoder.Decode(&req); err != nil {
    logger.Fatalf("Failed to decode to stdin: %s", err)
    return
  }

  // Perform the check with the given request
  res, err := Check(req)
  if err != nil {
    logger.Fatalf("Failed to connect to Github: %s", err)
    return
  }

  var encoder = json.NewEncoder(os.Stdout)

  // Generate a compatible Concourse output
  if err := doOutput(*res, encoder, logger); err != nil {
    logger.Fatalf("Failed to encode to stdout: %s", err)
    return
  }
}

func Check(req CheckRequest) (*CheckResponse, error) {
  client, err := api.NewGithubClient(
    req.Source.Repository,
    req.Source.AccessToken,
    req.Source.SkipSSLVerification,
    req.Source.GithubEndpoint,
  )
  if err != nil {
    return nil, err
  }

  var versions CheckResponse
  var version *Version

  // Get all pull requests
  pulls, err := client.ListPullRequests()
  if err != nil {
    return nil, err
  }

  // Pre-emptively populate list of team members so we can quickly look up
  // user association as we iterate over reviews and comments of PRs.
  // var userTeam map[string][]string
  if len(pulls) > 0 {
    if len(req.Source.ReviewerTeams) > 0 {
      for _, team := range req.Source.ReviewerTeams {
        _, err := client.ListTeamMembers(team)
        if err != nil {
          return nil, err
        }
      }
    }
  }

  // Iterate over all pull requests
  for _, pull := range pulls {
    version = &Version{
      PrID: strconv.Itoa(*pull.Number),
    }

    // Ignore if state not requested
    if !req.Source.requestsState(*pull.State) {
      continue
    }

    // Ignore if labels not requested
    if !req.Source.requestsLabels(pull.Labels) {
      continue
    }

    // Ignore if only mergeables requested
    if req.Source.OnlyMergeable && !*pull.Mergeable {
      continue
    }

    // Ignore drafts
    if *pull.Draft {
      continue
    }

    // Iterate through all the comments for this PR
    comments, err := client.ListPullRequestComments(int(*pull.Number))
    if err != nil {
      return nil, err
    }

    for _, comment := range comments {
      if req.Source.requestsApproverRegex(*comment.Body) {
        if req.Source.requestsApproverTeam(client, *pull, *comment.User.Login) {
          if !req.Source.requestsApproveState("comment") {
            continue
          }

          if comment.CreatedAt.Unix() > version.lastUpdated {
            version.lastUpdated = comment.CreatedAt.Unix()
          }

          version.approvedBy = append(version.approvedBy, &Response{
            CreatedAt: strconv.FormatInt(comment.CreatedAt.Unix(), 10),
            CommentID: strconv.FormatInt(*comment.ID, 10),
          })
        }
      }

      if req.Source.requestsReviewerRegex(*comment.Body) {
        if req.Source.requestsReviewerTeam(client, *pull, *comment.User.Login) {
          if comment.CreatedAt.Unix() > version.lastUpdated {
            version.lastUpdated = comment.CreatedAt.Unix()
          }

          version.reviewedBy = append(version.reviewedBy, &Response{
            CreatedAt: strconv.FormatInt(comment.CreatedAt.Unix(), 10),
            CommentID: strconv.FormatInt(*comment.ID, 10),
          })
        }
      }
    }

    // Iterate through all the reviews for this PR
    reviews, err := client.ListPullRequestReviews(int(*pull.Number))
    if err != nil {
      return nil, err
    }

    for _, review := range reviews {
      if req.Source.requestsApproverRegex(*review.Body) {
        if req.Source.requestsApproverTeam(client, *pull, *review.User.Login) {
          if !req.Source.requestsApproveState(*review.State) {
            continue
          }

          if review.SubmittedAt.Unix() > version.lastUpdated {
            version.lastUpdated = review.SubmittedAt.Unix()
          }

          version.approvedBy = append(version.approvedBy, &Response{
            CreatedAt: strconv.FormatInt(review.SubmittedAt.Unix(), 10),
            ReviewID: strconv.FormatInt(*review.ID, 10),
          })
        }
      }

      if req.Source.requestsReviewerRegex(*review.Body) {
        if req.Source.requestsReviewerTeam(client, *pull, *review.User.Login) {
          if !req.Source.requestsReviewState(*review.State) {
            continue
          }

          if review.SubmittedAt.Unix() > version.lastUpdated {
            version.lastUpdated = review.SubmittedAt.Unix()
          }

          version.reviewedBy = append(version.reviewedBy, &Response{
            CreatedAt: strconv.FormatInt(review.SubmittedAt.Unix(), 10),
            ReviewID: strconv.FormatInt(*review.ID, 10),
          })
        }
      }
    }

    // Only save the version if it matches the desired state:
    if req.Source.hasMinApprovers(len(version.approvedBy)) && 
       req.Source.hasMinReviewers(len(version.reviewedBy)) {
      
      // Convert responses to JSON string
      var out []byte
      out, err = json.Marshal(version.approvedBy)
      if err != nil {
        return nil, fmt.Errorf("could not marshal JSON: %s", err)
      }
      version.ApprovedBy = string(out)

      out, err = json.Marshal(version.reviewedBy)
      if err != nil {
        return nil, fmt.Errorf("could not marshal JSON: %s", err)
      }
      version.ReviewedBy = string(out)
      
      versions = append(versions, *version)
    }
  }

  sort.Slice(versions, func(i, j int) bool {
    return versions[i].lastUpdated < versions[j].lastUpdated
  })

  return &versions, nil
}
