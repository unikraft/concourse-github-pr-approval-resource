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
  "time"
  "regexp"
  "strconv"
  "io/ioutil"
  "encoding/json"
  "path/filepath"

  "github.com/spf13/cobra"
  "github.com/unikraft/concourse-github-pr-approval-resource/api"
)

// InCmd
var InCmd = &cobra.Command{
  Use:                   "in [OPTIONS] PATH",
  Short:                 "Run the input parsing step",
  Run:                   doInCmd,
  Args:                  cobra.ExactArgs(1),
  DisableFlagsInUseLine: true,
}

// InParams are the parameters for configuring the input
type InParams struct {
  SourcePath      string `json:"source_path"`
  GitDepth        int    `json:"git_depth"`
  Submodules      bool   `json:"submodules"`
  SkipDownload    bool   `json:"skip_download"`
  FetchTags       bool   `json:"fetch_tags"`
  IntegrationTool string `json:"integration_tool"`
  MapMetadata     bool   `json:"map_metadata"`
}

// InRequest from the check stdin.
type InRequest struct {
  Source  Source   `json:"source"`
  Version Version  `json:"version"`
  Params  InParams `json:"params"`
}

// InResponse represents the structure Concourse expects on stdout
type InResponse struct {
  Version  Version  `json:"version"`
  Metadata Metadata `json:"metadata"`
}

type Message struct {
  CommentID         int64             `json:"comment_id"`
  ReviewID          int64             `json:"review_id"`
  Body              string            `json:"body"`
  CreatedAt         time.Time         `json:"created_at"`
  UpdatedAt         time.Time         `json:"updated_at"`
  AuthorAssociation string            `json:"author_association"`
  HTMLURL           string            `json:"html_url"`
  UserLogin         string            `json:"user_login"`
  UserID            int64             `json:"user_id"`
  UserAvatarURL     string            `json:"user_avatar_url"`
  UserHTMLURL       string            `json:"user_html_url"`
  Matches           map[string]string `json:"match"`
}

type InMetadata struct {
  PRID              int       `json:"pr_id"`
  PRHeadRef         string    `json:"pr_head_ref"`
  PRHeadSHA         string    `json:"pr_head_sha"`
  PRBaseRef         string    `json:"pr_base_ref"`
  PRBaseSHA         string    `json:"pr_base_sha"`
  TotalApprovals    int       `json:"total_approvals"`
  TotalReviews      int       `json:"total_reviews"`
}

var (
  gh *api.GithubClient
)

func doInCmd(cmd *cobra.Command, args []string) {
  decoder := json.NewDecoder(os.Stdin)
  decoder.DisallowUnknownFields()
  
  // Concourse passes .json on stdin
  var req InRequest
  if err := decoder.Decode(&req); err != nil {
    logger.Fatal(err)
    return
  }
  
  // Perform the in command with the given request
  res, err := In(args[0], req)
  if err != nil {
    logger.Fatal(err)
    return
  }

  var encoder = json.NewEncoder(os.Stdout)

  // Generate a compatible Concourse output
  if err := doOutput(res, encoder, logger); err != nil {
    logger.Fatalf("Failed to encode to stdout: %s", err)
    return
  }
}

func In(outputDir string, req InRequest) (*InResponse, error) {
  var err error

  gh, err = api.NewGithubClient(
    req.Source.Repository,
    req.Source.AccessToken,
    req.Source.SkipSSLVerification,
    req.Source.GithubEndpoint,
  )
  if err != nil {
    return nil, err
  }

  prID, _ := strconv.ParseInt(req.Version.PrID, 10, 64)

  pull, err := gh.GetPullRequest(int(prID))
  if err != nil {
    return nil, err
  }

  metadata := InMetadata{
    PRID:           int(prID),
    PRHeadRef:     *pull.Head.Ref,
    PRHeadSHA:     *pull.Head.SHA,
    PRBaseRef:     *pull.Base.Ref,
    PRBaseSHA:     *pull.Base.SHA,
    TotalApprovals: 0,
    TotalReviews:   0,
  }

  // Write approvals, reviews, version and metadata for reuse in PUT path
  path := filepath.Join(outputDir)
  if err := os.MkdirAll(path, os.ModePerm); err != nil {
    return nil, fmt.Errorf("failed to create output directory: %s", err)
  }

  var reviewID int64
  var commentID int64
  var approvedBy []Message
  var reviewedBy []Message
  var message *Message

  // Decode the JSON value for approvers
  if err := json.Unmarshal([]byte(req.Version.ApprovedBy),
      &req.Version.approvedBy); err != nil {
    return nil, fmt.Errorf("could not unmarshal JSON: %s", err)
  }

  for i, approval := range req.Version.approvedBy {
    reviewID, _ = strconv.ParseInt(approval.ReviewID, 10, 64)
    commentID, _ = strconv.ParseInt(approval.CommentID, 10, 64)

    if reviewID > 0 {
      message, err = parseReview(int(prID), reviewID, req.Source.ApproverComments)
    } else if commentID > 0 {
      message, err = parseComment(commentID, req.Source.ApproverComments)
    } else {
      err = fmt.Errorf("invalid approval: no comment or review id")
    }

    if err != nil {
      return nil, fmt.Errorf("could not parse: %s", err)
    }

    err = saveMessage(path, i, message)
    if err != nil {
      return nil, fmt.Errorf("could not save message: %s", err)
    }

    approvedBy = append(approvedBy, *message)
    metadata.TotalApprovals++
  }

  // Decode the JSON value for reviewers
  if err := json.Unmarshal([]byte(req.Version.ReviewedBy),
      &req.Version.reviewedBy); err != nil {
    return nil, fmt.Errorf("could not unmarshal JSON: %s", err)
  }

  for i, review := range req.Version.reviewedBy {
    reviewID, _ = strconv.ParseInt(review.ReviewID, 10, 64)
    commentID, _ = strconv.ParseInt(review.CommentID, 10, 64)

    if reviewID > 0 {
      message, err = parseReview(int(prID), reviewID, req.Source.ReviewerComments)
    } else if commentID > 0 {
      message, err = parseComment(commentID, req.Source.ReviewerComments)
    } else {
      err = fmt.Errorf("invalid review: no comment or review id")
    }

    if err != nil {
      return nil, fmt.Errorf("could not parse: %s", err)
    }

    err = saveMessage(path, i, message)
    if err != nil {
      return nil, fmt.Errorf("could not save message: %s", err)
    }

    reviewedBy = append(reviewedBy, *message)
    metadata.TotalReviews++
  }

  serializedMetadata := serializeStruct(metadata)
  
  for i, approval := range approvedBy {
    for k, v := range approval.Matches {
      serializedMetadata.Add(fmt.Sprintf("%s_%d", k, i + 1), v)
    }
  }
  
  for i, review := range reviewedBy {
    for k, v := range review.Matches {
      serializedMetadata.Add(fmt.Sprintf("%s_%d", k, i + 1), v)
    }
  }

  b, err := json.Marshal(req.Version)
  if err != nil {
    return nil, fmt.Errorf("failed to marshal version: %s", err)
  }

  if err := ioutil.WriteFile(filepath.Join(path, "version.json"), b, 0644); err != nil {
    return nil, fmt.Errorf("failed to write version: %s", err)
  }

  b, err = json.Marshal(serializedMetadata)
  if err != nil {
    return nil, fmt.Errorf("failed to marshal metadata: %s", err)
  }

  if err := ioutil.WriteFile(filepath.Join(path, "metadata.json"), b, 0644); err != nil {
    return nil, fmt.Errorf("failed to write metadata: %s", err)
  }

  // Save the individual metadata items to seperate files
  for _, d := range serializedMetadata {
    filename := d.Name
    content := []byte(d.Value)
    if err := ioutil.WriteFile(filepath.Join(path, filename), content, 0644); err != nil {
      return nil, fmt.Errorf("failed to write metadata file %s: %s", filename, err)
    }
  }

  if req.Params.MapMetadata {
    err = writeMap(approvedBy, filepath.Join(path, "approval"))
    if err != nil {
      return nil, fmt.Errorf("cannot write map: %s", err)
    }

    err = writeMap(reviewedBy, filepath.Join(path, "review"))
    if err != nil {
      return nil, fmt.Errorf("cannot write map: %s", err)
    }
  }

  if !req.Params.SkipDownload {
    // Set the destination path to save the HEAD of the PR
    sourcePath := "source"
    if req.Params.SourcePath != "" {
      sourcePath = req.Params.SourcePath
    }

    sourcePath = filepath.Join(path, sourcePath)
    if err := os.MkdirAll(sourcePath, os.ModePerm); err != nil {
      return nil, fmt.Errorf("failed to create source directory: %s", err)
    }

    git, err := api.NewGitClient(
      req.Source.AccessToken,
      req.Source.SkipSSLVerification,
      req.Source.DisableGitLfs,
      sourcePath,
      os.Stderr,
    )
    if err != nil {
      return nil, fmt.Errorf("failed to initialize git client: %s", err)
    }

    // Initialize and pull the base for the PR
    if err := git.Init(*pull.Base.Ref); err != nil {
      return nil, fmt.Errorf("failed to initialize git repo: %s", err)
    }

    if err := git.Pull(
      *pull.Base.Repo.GitURL,
      *pull.Base.Ref,
      req.Params.GitDepth,
      req.Params.Submodules,
      req.Params.FetchTags,
    ); err != nil {
      return nil, err
    }

    // Fetch the PR and merge the specified commit into the base
    if err := git.Fetch(
      *pull.Base.Repo.GitURL,
      *pull.Number,
      req.Params.GitDepth,
      req.Params.Submodules,
    ); err != nil {
      return nil, err
    }

    switch tool := req.Params.IntegrationTool; tool {
    case "rebase", "":
      if err := git.Rebase(
        *pull.Base.Ref,
        *pull.Head.SHA,
        req.Params.Submodules,
      ); err != nil {
        return nil, err
      }
    case "merge":
      if err := git.Merge(
        *pull.Head.SHA,
        req.Params.Submodules,
      ); err != nil {
        return nil, err
      }
    case "checkout":
      if err := git.Checkout(
        *pull.Head.Ref,
        *pull.Head.SHA,
        req.Params.Submodules,
      ); err != nil {
        return nil, err
      }
    default:
      return nil, fmt.Errorf("invalid integration tool specified: %s", tool)
    }
  }

  return &InResponse{
    Version:  req.Version,
    Metadata: serializedMetadata,
  }, nil
}

func getParams(regEx, body string) (paramsMap map[string]string) {
  var compRegEx = regexp.MustCompile(regEx)
  match := compRegEx.FindStringSubmatch(body)

  paramsMap = make(map[string]string)
  for i, name := range compRegEx.SubexpNames() {
    if i > 0 && i <= len(match) {
      paramsMap[name] = match[i]
    }
  }

  return paramsMap
}

func parseReview(prID int, reviewID int64, regex []string) (*Message, error) {
  review, err := gh.GetPullRequestReview(
    prID,
    reviewID,
  )
  if err != nil {
    return nil, fmt.Errorf("could not retrieve review: %s", err)
  }

  message := &Message{
    ReviewID:         *review.ID,
    Body:              *review.Body,
    CreatedAt:         *review.SubmittedAt,
    AuthorAssociation: *review.AuthorAssociation,
    HTMLURL:           *review.HTMLURL,
    UserLogin:         *review.User.Login,
    UserID:            *review.User.ID,
    UserAvatarURL:     *review.User.AvatarURL,
    UserHTMLURL:       *review.User.HTMLURL,
  }
  
  message.Matches = make(map[string]string)
  for _, r := range regex {
    for k, v := range getParams(r, *review.Body) {
      message.Matches[k] = v
    }
  }

  return message, nil
}

func parseComment(commentID int64, regex []string) (*Message, error) {
  comment, err := gh.GetPullRequestComment(
    commentID,
  )
  if err != nil {
    return nil, fmt.Errorf("could not retrieve review: %s", err)
  }

  message := &Message{
    CommentID:         *comment.ID,
    Body:              *comment.Body,
    CreatedAt:         *comment.CreatedAt,
    UpdatedAt:         *comment.UpdatedAt,
    AuthorAssociation: *comment.AuthorAssociation,
    HTMLURL:           *comment.HTMLURL,
    UserLogin:         *comment.User.Login,
    UserID:            *comment.User.ID,
    UserAvatarURL:     *comment.User.AvatarURL,
    UserHTMLURL:       *comment.User.HTMLURL,
  }
  
  message.Matches = make(map[string]string)
  for _, r := range regex {
    for k, v := range getParams(r, *comment.Body) {
      message.Matches[k] = v
    }
  }
  return message, nil
}

// saveMessage ...
func saveMessage(path string, id int, message *Message) error {
  return nil
}

// writeMap writes the metadata of a review or comment to the parent path
func writeMap(messages []Message, parent string) error {
  var err error
  var dir string
  var path string
  var serialized Metadata

  for i, message := range messages {
    dir = filepath.Join(parent, fmt.Sprintf("%d", i + 1))
    if err := os.MkdirAll(dir, os.ModePerm); err != nil {
      return fmt.Errorf("failed to create source directory: %s", err)
    }

    serialized = serializeStruct(message)
    
    for _, field := range serialized {
      if field.Name == "match" {
        continue
      }

      path = filepath.Join(dir, field.Name)

      err = writeTextToFile(path, field.Value)
      if err != nil {
        return fmt.Errorf("could not write: %s: %s", path, err)
      }
    }

    for k, v := range message.Matches {
      path = filepath.Join(dir, k)

      err = writeTextToFile(path, v)
      if err != nil {
        return fmt.Errorf("could not write: %s: %s", path, err)
      }
    }
  }

  return nil
}

// writeTextToFile ...
func writeTextToFile(file, text string) error {
  // Write the comment body to the specified path
  f, err := os.Create(file)
  if err != nil {
    return fmt.Errorf("could not create comment file: %s", err)
  }

  defer f.Close()

  err = f.Truncate(0)
  if err != nil {
    return err
  }

  _, err = f.WriteString(text)
  if err != nil {
    return err
  }

  return nil
}
