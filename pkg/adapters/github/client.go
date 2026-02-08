/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package github

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v75/github"
)

// Client wraps the GitHub API client with app authentication.
type Client struct {
	gh             *gh.Client
	installationID int64
}

// NewClient creates a new GitHub client authenticated as a GitHub App installation.
func NewClient(appID, installationID int64, privateKeyPath string) (*Client, error) {
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading private key: %w", err)
	}

	transport, err := ghinstallation.New(http.DefaultTransport, appID, installationID, keyData)
	if err != nil {
		return nil, fmt.Errorf("creating installation transport: %w", err)
	}

	return &Client{
		gh:             gh.NewClient(&http.Client{Transport: transport}),
		installationID: installationID,
	}, nil
}

// newClientFromGH creates a Client from an existing go-github client (for testing).
func newClientFromGH(ghClient *gh.Client) *Client {
	return &Client{gh: ghClient}
}

// PostComment posts a comment to an issue or pull request.
func (c *Client) PostComment(ctx context.Context, owner, repo string, number int, body string) error {
	comment := &gh.IssueComment{Body: gh.Ptr(body)}
	_, _, err := c.gh.Issues.CreateComment(ctx, owner, repo, number, comment)
	if err != nil {
		return fmt.Errorf("creating comment: %w", err)
	}
	return nil
}

// GetIssue retrieves an issue or PR by number.
func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (*gh.Issue, error) {
	issue, _, err := c.gh.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("getting issue: %w", err)
	}
	return issue, nil
}

// ListIssueComments retrieves all comments on an issue.
func (c *Client) ListIssueComments(ctx context.Context, owner, repo string, number int) ([]*gh.IssueComment, error) {
	var allComments []*gh.IssueComment
	opts := &gh.IssueListCommentsOptions{
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		comments, resp, err := c.gh.Issues.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, fmt.Errorf("listing comments: %w", err)
		}
		allComments = append(allComments, comments...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allComments, nil
}
