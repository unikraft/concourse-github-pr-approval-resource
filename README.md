# concourse-github-pr-comment-resource

![resource-pipeline](https://github.com/unikraft/concourse-github-pr-approval-resource/workflows/resource-pipeline/badge.svg)

This Concourse resource monitors incoming comments or reviews on a Github Pull
Request and is able to monitor for approvals as well as reviews.  The resource
is designed to aid and automate code review and upstreaming for the [Unikraft
Open-Source Project](https://unikraft.org).

## Source configuration

The following parameters are used for the resource's `source` configuration:

| Parameter               | Required | Example                                     | Default                  | Description                                                                                                                                                                                                                                   |
| ----------------------- | -------- | ------------------------------------------- | ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `repository`            | Yes      | `nderjung/limp`                             |                          | The repository to listen for PR comments on.                                                                                                                                                                                                  |
| `disable_git_lfs`       | No       | `true`                                      | `false`                  | Disable Git LFS, skipping an attempt to convert pointers of files tracked into their corresponding objects when checked out into a working copy.                                                                                              |
| `access_token`          | Yes      |                                             |                          | The [personal access token](https://github.com/settings/tokens/new) of the account used to access, monitor and post comments on the repository in question.                                                                                   |
| `github_endpoint`       | No       |                                             | `https://api.github.com` | Endpoint used to connect to the Github v3 API.                                                                                                                                                                                                |
| `skip_ssl`              | No       | `true`                                      | `false`                  | Whether to skip SSL verification of the Github API.                                                                                                                                                                                           |
| `only_mergeable`        | No       | `true`                                      | `false`                  | Whether to react to (non-)mergeable pull requests.                                                                                                                                                                                            |
| `states`                | No       | `["closed"]`                                | `["open"]`               | The state of the pull request to react on.                                                                                                                                                                                                    |
| `ignore_states`         | No       | `["open"]`                                  | `[]`                     | The state of the pull request to not react on.                                                                                                                                                                                                |
| `labels`                | No       | `["bug"]`                                   | `[]`                     | The labels of the pull request to react on.                                                                                                                                                                                                   |
| `ignore_labels`         | No       | `["lifecycle/stale"]`                       | `[]`                     | The labels of the pull request not to react on.                                                                                                                                                                                               |
| `approver_comments`     | Yes      | `["Approved-by: (?P<aproved_by>.*>)"]`      | `[]`                     | The matching regular expression which an approver writes in a PR comment or review.                                                                                                                                                           |
| `approver_team`         | No       | `["@unikraft/maintainers-fallback"]`        | `[]`                     | The list of teams an approver must be a part of in order for the comment or review to be reccognsied as valid.                                                                                                                                |
| `min_approvals`         | No       | `1`                                         | `1`                      | The minimum number of approvals required for the PR to be acceppted.                                                                                                                                                                          | 
| `reviewer_team`         | no       | `["@unikraft/reviewer-fallback"]`           | `[]`                     | The matching regular expression which an reviewer writes in a PR comment or review.                                                                                                                                                           |
| `reviewer_comment`      | Yes      | `["Reviewed-by: (?P<reviewed_by>.*>)"]`     | `[]`                     | The list of teams an reviewer must be a part of in order for the comment or review to be reccognsied as valid.                                                                                                                                |
| `review_states`         | No       | `["commented", "changes_requested"]`        | `[]`                     | The state of the review, any combination of `approved`, `changes_requeste` and/or `commented`.                                                                                                                                                |
| `min_reviews`           | No       | `1`                                         | `1`                      | The minimum number of reviews required for the PR to be acceppted.                                                                                                                                                                            | 

## Behaviour

### `check`

Produces new versions for a pull request which match the criterial of minimum
number of reviews **and** approvals, set by `min_reviews` and `min_approvals`,
respectively.  Reviews and approvals must match the regular expression, and if
set, the user commenting or giving the review must be in at least one of the
specifiedd teams.

### `in`

The following parameters may be used in the `get` step of the resource:

| Parameter          | Required | Default       | Description                                                                  |
| ------------------ | -------- | ------------- | ---------------------------------------------------------------------------- |
| `source_path`      | No       | `source`      | The path to save the source within the resource.                             |
| `git_depth`        | No       | `0`           | Git clone depth.                                                             |
| `submodules`       | No       | `false`       | Whether to clone Git submodules.                                             |
| `fetch_tags`       | No       | `false`       | Whether to fetch Git tags.                                                   |
| `integration_tool` | No       | `rebase`      | How to merge the PR source, selection between `rebase`, `merge`, `checkout`. |
| `skip_download`    | No       | `false`       | Does not clone the pull request.                                             |
| `map_metadata`     | No       | `false`       | Whether to write the metadata values to file.                                |

The `in` procedure of this resource retrieves the following metadata about the
pull request.  If `map_metadata` is set to `true`, the values are saved to a
file with the same name as the key.

| Key                  | Description                                                                  |
| -------------------- | ---------------------------------------------------------------------------- |
| `pr_id`              | The ID of the pull request relative to the repository.                       |
| `pr_head_ref`        | The branch name from the HEAD of Pull Request.                               |
| `pr_head_sha`        | The commit SHA from the HEAD of the Pull Request.                            |
| `pr_base_ref`        | The branch name from the base of the Pull Request.                           |
| `pr_base_sha`        | The commit SHA from the base of the Pull Request.                            |
| `total_reviews`      | The total number of reviews this PR has received.                            |
| `total_approvals`    | The total number of approvals this PR has received.                          |

In addition to the metadata listed above, any regular expression containing a
named attributed compatible with [Golang's regular expression group
naming](https://golang.org/pkg/regexp/syntax/) will be saved too and suffix of
`_` and the numeric ID.  In the example where regular expressions for reviews
are: `"Reviewed-by: (?P<reviewed_by>.*>)"`, for every matching review, the
metadata key will be `reviewed_by_1`, `reviewed_by_2`, etc.

Additionally, the `in`/get step of this resource produces an additional JSON
formatted files which contain information about the PR comment or review:

 * `version.json` which contains only contains the unique ID of the Github
   comment to the PR; and,
 * `metadata.json` which contains a serialized version of the table above.

### `out`

| Parameter             | Required | Example           | Default | Description                                                         |
| --------------------- | -------- | ----------------- | ------- | ------------------------------------------------------------------- |
| `path`                | Yes      | `pr-comment`      |         | The name given to the resource in a in/get step.                    |
| `state`               | No       | `closed`          |         | The state to set the PR.  Options include `open` and `closed`.      |
| `comment`             | No       | `pong`            |         | The string to use as a new comment on the PR.                       |
| `comment_file`        | No       | `pong.txt`        |         | The path to the file to read and post as a new comment on the PR.   |
| `labels`              | No       | `[""]`            |         | The finite set of labels to replace on the PR.                      |
| `add_labels`          | No       | `["cicd/tested"]` |         | Additional labels to add to the PR.                                 |
| `remove_labels`       | No       | `["cicd/await"]`  |         | Labels to remove from the PR.                                       |
| `delete_last_comment` | No       | `true`            | `false` | Whether or not to delete the last comment of the PR comment thread. |


Note that `comment` and `comment_file` will all expand all [Concourse environment variables](https://concourse-ci.org/implementing-resource-types.html#resource-metadata).

#### Notes

 * The author of the comment will be that of the user whose access token is used
   in the resource's `source` configuration.
