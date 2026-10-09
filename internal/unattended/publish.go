package unattended

import (
	"context"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/surfaces/checkrun"
	"github.com/joshblazer/buggies/internal/surfaces/prcomment"
)

// Publish posts a review's result to a pull request: one review with an inline
// comment per finding (and a verification-first summary), plus the CI check run
// whose conclusion is the merge gate — failure iff a reproduced finding is
// data-loss or exploitable, advisory otherwise.
//
// It is the single place the unattended Post step renders GitHub output, so the
// exact requests the daemon sends are covered by one test against a mock GitHub
// rather than only wired inside serve. The client is already authenticated
// (installation token in production, a PAT in tests).
func Publish(ctx context.Context, cl *github.Client, pr github.PR, headSHA string, findings []finding.Finding) (reviewURL string, err error) {
	comments := make([]prcomment.Comment, 0, len(findings))
	for _, f := range findings {
		comments = append(comments, prcomment.Render(f))
	}
	url, err := cl.PostReview(ctx, pr, headSHA, prcomment.Summary(findings), comments)
	if err != nil {
		return "", err
	}
	cr := checkrun.Render(findings)
	if _, err := cl.UpsertCheckRun(ctx, pr, github.CheckRun{
		Name: cr.Name, HeadSHA: headSHA, Conclusion: cr.Conclusion, Title: cr.Title, Summary: cr.Summary,
	}); err != nil {
		return url, err
	}
	return url, nil
}
