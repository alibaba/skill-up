package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

// skillRefPattern matches references/, assets/, scripts/ paths cited in
// markdown prose — the conventional Agent Skill attachment directories. A
// match must start at a boundary (the beginning of a text segment, or after a
// character that cannot be part of a URL, a home-dir path, or a longer path)
// and ends on a word character so trailing sentence punctuation (.,;:!?。)
// and closing brackets are never captured. Group 1 is the path.
var skillRefPattern = regexp.MustCompile(`(?:^|[^\w./~-])((?:references|assets|scripts)/[\w](?:[\w./-]*[\w])?)`)

// skillRefPrefixes are the attachment directories a citation must point into.
var skillRefPrefixes = []string{"references/", "assets/", "scripts/"}

// linkSchemePattern matches the scheme prefix of a URI (https:, mailto:, ...).
var linkSchemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// CheckSkillIntegrity inspects the SKILL.md of the skill rooted at skillDir
// and returns one human-readable warning per finding: a missing or
// unterminated YAML frontmatter block, an empty name or description field,
// and references/, assets/, scripts/ paths cited in the markdown body that do
// not exist on disk. The body is parsed as CommonMark, so only real citations
// are collected — prose text, local link and image destinations, and inline
// code spans that cite a path. Code blocks, remote URLs, and raw HTML are
// never treated as references.
//
// It returns nil when skillDir has no SKILL.md (the directory is not a skill
// root, so there is nothing to check). Every finding is a warning; callers
// decide whether to tolerate or enforce them (skill-up validate --strict).
func CheckSkillIntegrity(skillDir string) []string {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("SKILL.md: unreadable: %v", err)}
	}

	var warnings []string
	body := data

	lines := bytes.Split(data, []byte("\n"))
	hasOpeningFence := len(lines) > 0 && isFence(lines[0])
	frontmatter, rest, closed := splitFrontmatter(lines)
	switch {
	case !hasOpeningFence:
		warnings = append(warnings, "SKILL.md: YAML frontmatter is missing (file must start with a --- fence)")
	case !closed:
		warnings = append(warnings, "SKILL.md: YAML frontmatter is not closed (missing closing --- fence)")
	default:
		warnings = append(warnings, frontmatterFieldWarnings(frontmatter)...)
		body = rest
	}

	for _, ref := range missingSkillRefs(skillDir, body) {
		warnings = append(warnings, fmt.Sprintf("SKILL.md: references %q but it does not exist on disk", ref))
	}

	return warnings
}

// splitFrontmatter extracts the YAML frontmatter block and the markdown body
// from SKILL.md content lines. ok is false when the first line is not a ---
// fence or no closing --- fence exists.
func splitFrontmatter(lines [][]byte) (frontmatter, body []byte, ok bool) {
	if len(lines) == 0 || !isFence(lines[0]) {
		return nil, nil, false
	}
	for i := 1; i < len(lines); i++ {
		if isFence(lines[i]) {
			return bytes.Join(lines[1:i], []byte("\n")), bytes.Join(lines[i+1:], []byte("\n")), true
		}
	}
	return nil, nil, false
}

// frontmatterFieldWarnings reports required SKILL.md frontmatter fields
// (name, description) that are missing or empty.
func frontmatterFieldWarnings(frontmatter []byte) []string {
	var meta skillFrontmatter
	if err := yaml.Unmarshal(frontmatter, &meta); err != nil {
		return []string{fmt.Sprintf("SKILL.md: YAML frontmatter is invalid: %v", err)}
	}

	var warnings []string
	if strings.TrimSpace(meta.Name) == "" {
		warnings = append(warnings, "SKILL.md: frontmatter field 'name' is missing or empty")
	}
	if strings.TrimSpace(meta.Description) == "" {
		warnings = append(warnings, "SKILL.md: frontmatter field 'description' is missing or empty")
	}
	return warnings
}

// missingSkillRefs returns the sorted, de-duplicated references/, assets/,
// scripts/ paths cited in body that do not exist under skillDir.
func missingSkillRefs(skillDir string, body []byte) []string {
	var missing []string
	for _, ref := range extractSkillRefs(body) {
		if _, err := os.Stat(filepath.Join(skillDir, filepath.FromSlash(ref))); err != nil {
			missing = append(missing, ref)
		}
	}
	slices.Sort(missing)
	return missing
}

// extractSkillRefs returns the references/, assets/, scripts/ paths cited in
// a markdown body, de-duplicated in order of first appearance. The body is
// parsed as CommonMark and citations are collected from prose text, from
// local link and image destinations, and from inline code spans that cite a
// path. Fenced and indented code blocks, remote URLs, autolinks, and raw HTML
// never yield references — they are documentation examples or external
// resources, not skill attachments.
func extractSkillRefs(body []byte) []string {
	doc := goldmark.New().Parser().Parse(text.NewReader(body))
	seen := make(map[string]struct{})
	var refs []string
	add := func(ref string) {
		if _, ok := seen[ref]; !ok {
			seen[ref] = struct{}{}
			refs = append(refs, ref)
		}
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Text:
			for _, m := range skillRefPattern.FindAllSubmatch(node.Segment.Value(body), -1) {
				add(string(m[1]))
			}
		case *ast.Link:
			if ref, ok := localSkillRef(node.Destination); ok {
				add(ref)
			}
		case *ast.Image:
			if ref, ok := localSkillRef(node.Destination); ok {
				add(ref)
			}
		case *ast.CodeSpan:
			// A code span whose first token is an attachment path cites that
			// path (`references/install.md`); a span that merely contains a
			// path inside a command (`cat scripts/x.sh`) is an example.
			if content := codeSpanText(node, body); citesRefPath(content) {
				for _, m := range skillRefPattern.FindAllStringSubmatch(content, -1) {
					add(m[1])
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// localSkillRef converts a link or image destination into a skill-local
// attachment path. ok is false for remote URLs, pure anchors, and
// destinations outside references/, assets/, scripts/.
func localSkillRef(dest []byte) (string, bool) {
	s := string(dest)
	if i := strings.IndexAny(s, "#?"); i >= 0 {
		s = s[:i]
	}
	if s == "" || strings.HasPrefix(s, "//") || linkSchemePattern.MatchString(s) || !hasRefPrefix(s) {
		return "", false
	}
	return s, true
}

// citesRefPath reports whether the first whitespace-separated token of s
// starts with one of the attachment directory prefixes.
func citesRefPath(s string) bool {
	if fields := strings.Fields(s); len(fields) > 0 {
		return hasRefPrefix(fields[0])
	}
	return false
}

// hasRefPrefix reports whether s points into references/, assets/, scripts/.
func hasRefPrefix(s string) bool {
	for _, prefix := range skillRefPrefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// codeSpanText concatenates the text of a code span's children.
func codeSpanText(span *ast.CodeSpan, src []byte) string {
	var buf bytes.Buffer
	for child := span.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			buf.Write(t.Segment.Value(src))
		}
	}
	return buf.String()
}
