package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/github"
	"github.com/spf13/viper"
	"golang.org/x/mod/semver"
	"golang.org/x/oauth2"
)

// This script should only run when PRs are merged into main. It links the merged PR as well as linked issues
// that were closed as a result of the merge, to the latest unreleased milestone (if exists and not already linked).

type GitHubIssue struct {
	Owner string
	Repo string
	Id int
}

func (g GitHubIssue) getMilestoneId(ctx context.Context, client *github.Client) (*int, error) {
	ghMilestones, _, err := client.Issues.ListMilestones(ctx, g.Owner, g.Repo, nil)
	if err != nil {
		return nil, fmt.Errorf("retrieving list of milestones: %+v", err)
	}

	milestones := make(map[string]int)

	for _, m := range ghMilestones {
		title := *m.Title
		r := regexp.MustCompile(`v[0-9]\.[0-9]+\.[0-9]`)
		if r.MatchString(title) && !strings.EqualFold(*m.State, "closed") {
			milestones[title[1:]] = *m.Number
		}
	}

	// TODO create milestone here?
	if len(milestones) == 0 {
		return nil, fmt.Errorf("no open version milestones were found")
	}

	var versions []string
	for title, _ := range milestones {
		versions = append(versions, title)
	}
	semver.Sort(versions)
	milestoneId := milestones[versions[0]]

	log.Printf("[DEBUG] lowest open version milestone: %s", versions[0])
	return &milestoneId, nil
}

func (g GitHubIssue) getLinkedIssue(ctx context.Context, client *github.Client) (*int, error) {
	resp, _, _ := client.Issues.Get(ctx, g.Owner, g.Repo, g.Id)

	if resp.Body != nil {
		bodySplit := strings.Split(*resp.Body, " ")
		keywords := regexp.MustCompile(`^[fF]ix(e)?(s)?(d)?$|[cC]lose(s)?(d)?$|[rR]esolve(s)?(d)?$`)
		issue := regexp.MustCompile(`^#[0-9]+`)

		for i, s := range bodySplit {
			if keywords.MatchString(s) {
				// check whether next element is the issue number
				next := bodySplit[i + 1]
				if issue.MatchString(next) {
					id, _ := strconv.Atoi(next[1:])
					return &id, nil
				}
			}
		}
	}

	log.Printf("[DEBUG] no special keywords found in issue description")
	return nil, nil
}

func (g GitHubIssue) updateMilestone(ctx context.Context, client *github.Client, milestoneId int) error {
	issue, _, err := client.Issues.Get(ctx, g.Owner, g.Repo, g.Id)
	if err != nil {
		return fmt.Errorf("getting issue #%d: %+v", g.Id, err)
	}

	if issue.Milestone == nil && strings.EqualFold(*issue.State, "closed") {
		_, _, err := client.Issues.Edit(ctx, g.Owner, g.Repo, g.Id, &github.IssueRequest{Milestone: &milestoneId})
		if err != nil {
			return fmt.Errorf("updating milestone on issue #%d: %+v", g.Id, err)
		}
		return nil
	}

	log.Printf("[DEBUG] github issue #%d already has milestone %s", g.Id, *issue.Milestone.Title)
	return nil
}

func newGitHubClient(token string) (*github.Client, context.Context) {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	return github.NewClient(tc), ctx
}

func run() error {
	viper.AutomaticEnv()
	token := viper.GetString("github_token")
	owner := strings.Split(viper.GetString("github_repository"), "/")[0]
	repo := strings.Split(viper.GetString("github_repository"), "/")[1]
	prId, err := strconv.Atoi(viper.GetString("pr_number"))
	if err != nil {
		return fmt.Errorf("parsing pr number: %+v", err)
	}

	pr := GitHubIssue{owner, repo, prId}
	client, ctx := newGitHubClient(token)

	milestoneId, err := pr.getMilestoneId(ctx, client)
	if err != nil {
		return fmt.Errorf("getting milestone id: %s", err)
	}
	if milestoneId == nil {
		log.Printf("[DEBUG] no open version milestones exists in github")
		return nil
	}

	if err = pr.updateMilestone(ctx, client, *milestoneId); err != nil {
		return err
	}

	liId, err := pr.getLinkedIssue(ctx, client)
	if err != nil {
		return fmt.Errorf("getting linked issues for #%d: %+v", pr.Id, err)
	}
	if liId != nil {
		li := GitHubIssue{owner, repo, *liId}
		if err = li.updateMilestone(ctx, client, *milestoneId); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}