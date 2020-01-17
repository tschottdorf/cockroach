// Copyright 2016 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package issues

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-github/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPost(t *testing.T) {
	const (
		assignee    = "hodor"
		milestone   = 2
		envTags     = "deadlock"
		envGoFlags  = "race"
		sha         = "abcd123"
		branch      = "release-123.45"
		serverURL   = "https://teamcity.example.com"
		buildID     = 8008135
		issueID     = 1337
		issueNumber = 30
	)

	unset := setEnv(map[string]string{
		teamcityVCSNumberEnv: sha,
		teamcityServerURLEnv: serverURL,
		teamcityBuildIDEnv:   strconv.Itoa(buildID),
		tagsEnv:              envTags,
		goFlagsEnv:           envGoFlags,
	})
	defer unset()

	testCases := []struct {
		name        string
		packageName string
		testName    string
		message     string
		artifacts   string
		author      string
	}{
		{
			name:        "failure",
			packageName: "github.com/cockroachdb/cockroach/pkg/storage",
			testName:    "TestReplicateQueueRebalance",
			message: "	<autogenerated>:12: storage/replicate_queue_test.go:103, condition failed to evaluate within 45s: not balanced: [10 1 10 1 8]",
			author: "bran",
		},
		{
			name:        "fatal",
			packageName: "github.com/cockroachdb/cockroach/pkg/storage",
			testName:    "TestGossipHandlesReplacedNode",
			message: `logging something
F170517 07:33:43.763059 69575 storage/replica.go:1360  [n3,s3,r1/3:/M{in-ax}] something bad happened:
foo
bar

goroutine 12 [running]:
  doing something

goroutine 13:
  hidden

`,
			author: "bran",
		},
		{
			name:        "panic",
			packageName: "github.com/cockroachdb/cockroach/pkg/storage",
			testName:    "TestGossipHandlesReplacedNode",
			message: `logging something
panic: something bad happened:

foo
bar

goroutine 12 [running]:
  doing something

goroutine 13:
  hidden

`,
			author: "bran",
		},
		{
			name:        "with-artifacts",
			packageName: "github.com/cockroachdb/cockroach/pkg/storage",
			testName:    "kv/splits/nodes=3/quiesce=true",
			message:     "The test failed on branch=master, cloud=gce:",
			artifacts:   "/kv/splits/nodes=3/quiesce=true",
			author:      "bran",
		},
	}

	const (
		foundNoIssue              = "no-issue"
		foundOnlyMatchingIssue    = "matching-issue"
		foundOneMismatchingIssue  = "mismatching-issue"
		foundTwoMismatchingIssues = "mismatching-issues"
		foundAllIssues            = "several-issues"
	)

	for _, c := range testCases {
		for _, foundIssue := range []string{
			foundNoIssue, foundOnlyMatchingIssue, foundOneMismatchingIssue, foundTwoMismatchingIssues, foundAllIssues,
		} {
			name := c.name + "-" + foundIssue
			t.Run(name, func(t *testing.T) {
				var buf strings.Builder
				p := &poster{}

				createdIssue := false
				p.createIssue = func(_ context.Context, owner string, repo string,
					issue *github.IssueRequest) (*github.Issue, *github.Response, error) {
					createdIssue = true
					body := *issue.Body
					issue.Body = nil
					_, _ = fmt.Fprintf(&buf, "createIssue owner=%s repo=%s %s:\n", owner, repo, github.Stringify(issue))
					_, _ = fmt.Fprintln(&buf, body)
					return &github.Issue{ID: github.Int64(issueID)}, nil, nil
				}

				matchingIssue := github.Issue{
					Number: github.Int(issueNumber),
					Labels: []github.Label{{
						Name: github.String("C-test-failure"),
					}, {
						Name: github.String("O-robot"),
					}, {
						Name: github.String("release-0.1"),
					}},
				}

				p.searchIssues = func(_ context.Context, query string,
					opt *github.SearchOptions) (*github.IssuesSearchResult, *github.Response, error) {
					result := &github.IssuesSearchResult{}

					mismatchingIssue1 := github.Issue{
						Number: github.Int(issueNumber + 1),
						Labels: []github.Label{{
							Name: github.String("C-test-failure"),
						}, {
							Name: github.String("O-robot"),
						}, {
							Name: github.String("release-0.2"), // here's the mismatch
						}},
					}

					mismatchingIssue2 := github.Issue{
						Number: github.Int(issueNumber + 2),
						Labels: []github.Label{{
							Name: github.String("C-test-failure"),
						}, {
							Name: github.String("O-robot"),
						}, {
							Name: github.String("release-0.3"), // here's the mismatch
						},
							{
								Name: github.String("release-blocker"), // here's the mismatch
							},
						},
					}

					switch foundIssue {
					case foundNoIssue:
					case foundOnlyMatchingIssue:
						result.Issues = []github.Issue{
							matchingIssue,
						}
					case foundOneMismatchingIssue:
						result.Issues = []github.Issue{
							mismatchingIssue2,
						}
					case foundTwoMismatchingIssues:
						result.Issues = []github.Issue{
							mismatchingIssue1,
							mismatchingIssue2,
						}
					case foundAllIssues:
						result.Issues = []github.Issue{
							mismatchingIssue2,
							matchingIssue,
							mismatchingIssue1,
						}
					default:
						t.Errorf("unhandled: %s", foundIssue)
					}
					result.Total = github.Int(len(result.Issues))
					_, _ = fmt.Fprintf(&buf, "searchIssue query=%s: result %s\n", query, github.Stringify(result))
					return result, nil, nil
				}

				createdComment := false
				p.createComment = func(
					_ context.Context, owner string, repo string, number int, comment *github.IssueComment,
				) (*github.IssueComment, *github.Response, error) {
					assert.Equal(t, *matchingIssue.Number, number)
					createdComment = true
					body := *comment.Body
					comment.Body = nil
					_, _ = fmt.Fprintf(&buf, "createComment owner=%s repo=%s issue=%d %s:\n", owner, repo, number, github.Stringify(comment))
					_, _ = fmt.Fprintln(&buf, body)
					return &github.IssueComment{}, nil, nil
				}

				p.listCommits = func(
					_ context.Context, owner string, repo string, opts *github.CommitsListOptions,
				) ([]*github.RepositoryCommit, *github.Response, error) {
					_, _ = fmt.Fprintf(&buf, "listCommits owner=%s repo=%s %s\n", owner, repo, github.Stringify(opts))
					assignee := assignee
					return []*github.RepositoryCommit{
						{
							Author: &github.User{
								Login: &assignee,
							},
						},
					}, nil, nil
				}

				p.listMilestones = func(_ context.Context, owner, repo string,
					_ *github.MilestoneListOptions) ([]*github.Milestone, *github.Response, error) {
					result := []*github.Milestone{
						{Title: github.String("3.3"), Number: github.Int(milestone)},
						{Title: github.String("3.2"), Number: github.Int(1)},
					}
					_, _ = fmt.Fprintf(&buf, "listMilestones owner=%s repo=%s: result %s\n", owner, repo, github.Stringify(result))
					return result, nil, nil
				}

				p.getLatestTag = func() (string, error) {
					const tag = "v3.3.0"
					_, _ = fmt.Fprintf(&buf, "getLatestTag: result %s\n", tag)
					return tag, nil
				}

				p.init()
				p.branch = branch

				ctx := context.Background()
				req := PostRequest{
					TitleTemplate: UnitTestFailureTitle,
					BodyTemplate:  UnitTestFailureBody,
					PackageName:   c.packageName,
					TestName:      c.testName,
					Message:       c.message,
					Artifacts:     c.artifacts,
					AuthorEmail:   c.author,
					ExtraLabels:   []string{"release-0.1"},
				}
				require.NoError(t, p.post(ctx, req))
				path := filepath.Join("testdata", name+".txt")
				b, err := ioutil.ReadFile(path)
				failed := !assert.NoError(t, err)
				if !failed {
					exp, act := string(b), buf.String()
					failed = failed || !assert.Equal(t, exp, act)
				}
				const rewrite = true
				if failed && rewrite {
					_ = os.MkdirAll(filepath.Dir(path), 0755)
					require.NoError(t, ioutil.WriteFile(path, []byte(buf.String()), 0644))
				}

				switch foundIssue {
				case foundNoIssue, foundOneMismatchingIssue, foundTwoMismatchingIssues:
					require.True(t, createdIssue)
					require.False(t, createdComment)
				case foundOnlyMatchingIssue, foundAllIssues:
					require.False(t, createdIssue)
					require.True(t, createdComment)
				default:
					t.Errorf("unhandled: %s", foundIssue)
				}
			})
		}
	}
}

func TestPostEndToEnd(t *testing.T) {
	t.Skip("only for manual testing")
	env := map[string]string{
		// githubAPITokenEnv must be set in your actual env.

		teamcityVCSNumberEnv: "deadbeef",
		teamcityServerURLEnv: "https://teamcity.cockroachdb.com",
		teamcityBuildIDEnv:   "12345",
		tagsEnv:              "-endtoendenv",
		goFlagsEnv:           "-somegoflags",
	}
	unset := setEnv(env)
	defer unset()

	// Adjust to your taste. Your token must have access and you must have a fork
	// of the cockroachdb/cockroach repo.
	githubUser = "tbg"

	req := PostRequest{
		TitleTemplate: "test issue 2",
		BodyTemplate:  "test body",
		PackageName:   "github.com/cockroachdb/cockroach/pkg/foo/bar",
		TestName:      "TestFooBarBaz",
		Message:       "I'm a message",
		AuthorEmail:   "tobias.schottdorf@gmail.com",
		ExtraLabels:   []string{"release-1.0", "release-blocker"},
	}

	require.NoError(t, Post(context.Background(), req))
}

func TestGetAssignee(t *testing.T) {
	listCommits := func(_ context.Context, owner string, repo string,
		opts *github.CommitsListOptions) ([]*github.RepositoryCommit, *github.Response, error) {
		return []*github.RepositoryCommit{
			{},
		}, nil, nil
	}
	_, _ = getAssignee(context.Background(), "", listCommits)
}

func TestInvalidAssignee(t *testing.T) {
	u, err := url.Parse("https://api.github.com/repos/cockroachdb/cockroach/issues")
	if err != nil {
		log.Fatal(err)
	}
	r := &github.ErrorResponse{
		Response: &http.Response{
			StatusCode: 422,
			Request: &http.Request{
				Method: "POST",
				URL:    u,
			},
		},
		Errors: []github.Error{{
			Resource: "Issue",
			Field:    "assignee",
			Code:     "invalid",
			Message:  "",
		}},
	}
	if !isInvalidAssignee(r) {
		t.Fatalf("expected invalid assignee")
	}
}

func setEnv(kv map[string]string) func() {
	undo := map[string]*string{}
	for key, value := range kv {
		val, ok := os.LookupEnv(key)
		if ok {
			undo[key] = &val
		} else {
			undo[key] = nil
		}

		if err := os.Setenv(key, value); err != nil {
			panic(err)
		}
	}
	return func() {
		for key, value := range undo {
			if value != nil {
				if err := os.Setenv(key, *value); err != nil {
					panic(err)
				}
			} else {
				if err := os.Unsetenv(key); err != nil {
					panic(err)
				}
			}
		}
	}
}
