package prcomment

import (
	"fmt"
	"strings"

	"github.com/buggies-dev/kernel/finding"
)

// Comment is one inline PR comment, anchored to a line on the head side.
type Comment struct {
	Path string
	Line int
	Body string
}

// Render turns a finding into its PR comment. Layout follows USAGE.md:
// claim, badge, collapsed proof, fix, honest confidence line.
func Render(f finding.Finding) Comment {
	var b strings.Builder

	fmt.Fprintf(&b, "**%s**\n\n", f.Claim)
	fmt.Fprintf(&b, "`%s · %s × %s`", title(string(f.Type)), title(f.Confidence.String()), title(f.Impact.String()))
	if f.Blocking() {
		b.WriteString(" · **blocking**")
	}
	b.WriteString("\n\n")

	if f.Confidence == finding.Reproduced && f.Proof != nil {
		ev := f.Proof.Result.Evidence()
		fmt.Fprintf(&b, "<details>\n<summary>Failing test — reproduced %d/%d runs in the sandbox</summary>\n\n",
			len(f.Proof.Result.Runs), len(f.Proof.Result.Runs))
		fmt.Fprintf(&b, "`%s`\n\n```%s\n%s\n```\n", f.Proof.Test.Path, fence(f.Proof.Test.Path), strings.TrimRight(string(f.Proof.Test.Content), "\n"))
		if ev.StackTrace != "" {
			fmt.Fprintf(&b, "\nOutput:\n```\n%s\n```\n", strings.TrimSpace(ev.StackTrace))
		}
		b.WriteString("\n</details>\n\n")

		if fix := strings.TrimSpace(f.Proof.SuggestedFix); fix != "" {
			fmt.Fprintf(&b, "<details>\n<summary>Suggested fix</summary>\n\n```diff\n%s\n```\n\n</details>\n\n", fix)
		}
		fmt.Fprintf(&b, "_Reproduced in sandbox (%s, %s)._", f.Proof.Result.Evidence().Image, f.Proof.Result.Outcome)
	} else {
		b.WriteString("_Static only — not yet reproduced. Treat as a lead, not a fact._")
	}
	fmt.Fprintf(&b, "\n\n<sub>buggies · `%s`</sub>", f.ID)

	return Comment{Path: f.Location.Path, Line: f.Location.Line, Body: b.String()}
}

// Summary is the review-level body: one line, verification first.
func Summary(findings []finding.Finding) string {
	if len(findings) == 0 {
		return "Buggies found nothing it could reproduce. No comment is the intended outcome for most changes."
	}
	blocking := 0
	for _, f := range findings {
		if f.Blocking() {
			blocking++
		}
	}
	s := fmt.Sprintf("Buggies reproduced %d finding(s) in the sandbox", len(findings))
	if blocking > 0 {
		s += fmt.Sprintf(", %d blocking", blocking)
	}
	return s + ". Each comment carries the failing test."
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func fence(path string) string {
	switch {
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"):
		return "ts"
	case strings.HasSuffix(path, ".py"):
		return "python"
	default:
		return "js"
	}
}
