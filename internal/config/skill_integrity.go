package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
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
// match must start at a boundary (the beginning of a text segment, after a
// character that cannot be part of a URL, a home-dir path, or a longer path,
// or after an explicit ./ prefix) and ends on a word character so trailing
// sentence punctuation (.,;:!?。) and closing brackets are never captured.
// Group 1 is the path.
var skillRefPattern = regexp.MustCompile(`(?:^|[^\w./~-]|\./)((?:references|assets|scripts)/[\w](?:[\w./-]*[\w])?)`)

// skillRefPrefixes are the attachment directories a citation must point into.
var skillRefPrefixes = []string{"references/", "assets/", "scripts/"}

// linkSchemePattern matches the scheme prefix of a URI (https:, mailto:, ...).
var linkSchemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// urlTokenPattern matches a complete URL token (scheme://...) so prose
// scanning can blank it out before collecting citations: a query string or
// fragment may contain an attachment-looking path (view?file=assets/x.png)
// that is part of the URL, not a skill citation.
var urlTokenPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://\S+`)

// relScriptPattern matches ./-prefixed relative path mentions inside script
// and data files (`source ./lib.sh`). Group 1 is the path relative to the
// mentioning file's own directory.
var relScriptPattern = regexp.MustCompile(`(?:^|[^\w./~-])\./([\w](?:[\w./-]*[\w])?)`)

// CheckSkillIntegrity inspects the SKILL.md of the skill rooted at skillDir
// and returns one human-readable warning per finding: a missing or
// unterminated YAML frontmatter block, an empty name or description field,
// references/, assets/, scripts/ paths cited in the markdown body that do not
// exist on disk, and files under those directories that are not reachable
// from the body. The body is parsed as CommonMark, so only real citations
// are collected — prose text, local link and image destinations, and inline
// code spans that cite a path. Code blocks, remote URLs, autolinks, and raw
// HTML never yield references — they are documentation examples or external
// resources, not skill attachments.
//
// Reachability is transitive: an attachment a cited markdown document links
// to, or one a cited script mentions, counts as cited — absence of a direct
// SKILL.md citation is not evidence that such a file is orphaned.
//
// It returns nil when skillDir has no SKILL.md (the directory is not a skill
// root, so there is nothing to check). Every finding is a warning; callers
// decide whether to tolerate or enforce them (skill-up validate --skill
// --strict).
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

	bodyRefs := extractSkillRefs(body)
	for _, ref := range missingSkillRefs(skillDir, bodyRefs) {
		warnings = append(warnings, fmt.Sprintf("SKILL.md: references %q but it does not exist on disk", ref))
	}
	for _, file := range uncitedSkillFiles(skillDir, collectReachableRefs(skillDir, bodyRefs)) {
		warnings = append(warnings, fmt.Sprintf("SKILL.md: never references %q but the file exists on disk", file))
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

// missingSkillRefs returns the sorted references/, assets/, scripts/ paths
// cited from the SKILL.md body that do not exist under skillDir. Only the
// body feeds this check: it is the document the skill author maintains, so
// its dangling citations are worth reporting, while attachments' own
// dependencies only feed the reachability set.
func missingSkillRefs(skillDir string, refs []string) []string {
	var missing []string
	for _, ref := range refs {
		if _, err := os.Stat(filepath.Join(skillDir, filepath.FromSlash(ref))); err != nil {
			missing = append(missing, ref)
		}
	}
	slices.Sort(missing)
	return missing
}

// collectReachableRefs walks the citation graph seeded by the SKILL.md body
// references and returns every skill-relative path reachable from the body:
// the cited paths themselves, every file beneath a cited directory, the
// local documents a cited markdown file links to, and the paths a cited
// script mentions. The uncited-attachment check uses this set so a file that
// is reachable through a cited attachment (SKILL.md → references/guide.md →
// references/details.md) is not mistaken for an orphan.
func collectReachableRefs(skillDir string, seeds []string) map[string]struct{} {
	cited := make(map[string]struct{}, len(seeds))
	scanned := make(map[string]struct{})
	var queue []string

	var enqueue func(rel string)
	enqueue = func(rel string) {
		abs := filepath.Join(skillDir, filepath.FromSlash(rel))
		// #nosec G703 -- callers confine rel to the skill root (normalizeSkillRef
		// rejects ../ escapes); reads during traversal are additionally
		// symlink-contained by readSkillLocalFile.
		info, err := os.Stat(abs)
		if err != nil {
			return // dangling citation: reported by the missing check; nothing to traverse
		}
		cited[rel] = struct{}{}
		if _, ok := scanned[rel]; ok {
			return
		}
		scanned[rel] = struct{}{}
		if info.IsDir() {
			enqueueDirContents(skillDir, abs, rel, enqueue)
			return
		}
		queue = append(queue, rel)
	}

	for _, seed := range seeds {
		enqueue(seed)
	}
	for len(queue) > 0 {
		rel := queue[0]
		queue = queue[1:]
		for _, dep := range extractAttachmentRefs(skillDir, rel) {
			enqueue(dep)
		}
	}
	return cited
}

// enqueueDirContents enqueues every non-dotfile beneath a cited directory,
// so documents under a directory citation get their own dependencies walked.
func enqueueDirContents(skillDir, root, rel string, enqueue func(string)) {
	// #nosec G703 -- root is a cited directory under skillDir; the walk only
	// enqueues paths still inside the skill root.
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are not traversal blockers
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		// p is always under skillDir, so Rel cannot fail in practice.
		if fileRel, _ := filepath.Rel(skillDir, p); filepath.ToSlash(fileRel) != rel {
			enqueue(filepath.ToSlash(fileRel))
		}
		return nil
	})
}

// extractAttachmentRefs returns the dependencies of one reachable file as
// clean root-relative slash paths. A markdown document contributes its local
// link and image destinations — root-anchored when they carry an attachment
// prefix (references/x.md), resolved against the document's own directory
// otherwise ([details](details.md) inside references/guide.md means
// references/details.md) — plus root-anchored prose and code-span citations.
// Any other text file (scripts, data files) contributes root-anchored
// citations and ./-relative path mentions resolved against its directory.
// Binary files yield nothing.
func extractAttachmentRefs(skillDir, rel string) []string {
	data, ok := readSkillLocalFile(skillDir, filepath.Join(skillDir, filepath.FromSlash(rel)))
	if !ok {
		return nil
	}

	seen := make(map[string]struct{})
	var refs []string
	dir := path.Dir(rel)
	add := func(dep string) {
		dep = path.Clean(dep)
		if dep == "." || dep == ".." || strings.HasPrefix(dep, "../") {
			return // escapes the skill root: not a skill-local dependency
		}
		if _, dup := seen[dep]; !dup {
			seen[dep] = struct{}{}
			refs = append(refs, dep)
		}
	}
	if isMarkdownRef(rel) {
		walkMarkdownAttachmentRefs(rel, data, add)
		return refs
	}

	if isBinaryContent(data) {
		return nil
	}
	collectProseRefs(data, add)
	for _, m := range relScriptPattern.FindAllSubmatch(data, -1) {
		add(path.Join(dir, string(m[1])))
	}
	return refs
}

// walkMarkdownAttachmentRefs collects the dependencies of a markdown
// attachment: local link and image destinations — root-anchored when they
// carry an attachment prefix (references/x.md), resolved against the
// document's own directory otherwise ([details](details.md) inside
// references/guide.md means references/details.md) — plus root-anchored
// prose and code-span citations, matching the SKILL.md body conventions.
func walkMarkdownAttachmentRefs(rel string, data []byte, add func(string)) {
	dir := path.Dir(rel)
	addLinkTarget := func(dest []byte) {
		target, ok := localLinkTarget(dest)
		if !ok {
			return
		}
		if norm, ok := normalizeSkillRef(target); ok {
			add(norm) // root-anchored attachment citation
			return
		}
		add(path.Join(dir, target)) // document-relative dependency
	}

	doc := goldmark.New().Parser().Parse(text.NewReader(data))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Text:
			collectProseRefs(node.Segment.Value(data), add)
		case *ast.Link:
			addLinkTarget(node.Destination)
		case *ast.Image:
			addLinkTarget(node.Destination)
		case *ast.CodeSpan:
			// Same convention as the SKILL.md body: a code span whose first
			// token is a path cites that path.
			if content := codeSpanText(node, data); citesRefPath(content) {
				for _, m := range skillRefPattern.FindAllStringSubmatch(content, -1) {
					add(m[1])
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
}

// readSkillLocalFile reads abs only when it resolves inside the skill root,
// so a symlinked attachment cannot leak the dependency scan into the wider
// filesystem.
func readSkillLocalFile(skillDir, abs string) ([]byte, bool) {
	root, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		return nil, false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, false
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return nil, false
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, false
	}
	return data, true
}

// isMarkdownRef reports whether rel names a markdown document.
func isMarkdownRef(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	return ext == ".md" || ext == ".markdown"
}

// isBinaryContent reports whether data looks like a binary file (a NUL byte
// in the first 8 KiB); dependency scanning skips binaries.
func isBinaryContent(data []byte) bool {
	const sniffLen = 8192
	if len(data) > sniffLen {
		data = data[:sniffLen]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// uncitedSkillFiles returns the sorted files that exist under the skill's
// attachment directories (references/, assets/, scripts/, recursively) but
// are not in cited — the set of paths reachable from the SKILL.md body.
// A citation that names a directory (references/ or references/advanced/)
// covers every file beneath it. Dotfiles and dot-directories (.DS_Store,
// .gitkeep, .git/) are local metadata and are never reported. The reverse
// direction matters because truncating the tail of SKILL.md silently turns
// the attachments it cited into orphans.
func uncitedSkillFiles(skillDir string, cited map[string]struct{}) []string {
	var orphans []string
	for _, dir := range skillRefPrefixes {
		root := filepath.Join(skillDir, filepath.FromSlash(dir))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // absent or unreadable attachment directory: no files to report
			}
			if d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			// path is always under skillDir-relative root, so Rel cannot
			// fail in practice.
			rel, _ := filepath.Rel(skillDir, path)
			if !isCitedAttachment(filepath.ToSlash(rel), cited) {
				orphans = append(orphans, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	slices.Sort(orphans)
	return orphans
}

// isCitedAttachment reports whether rel is reachable — cited exactly, or
// covered by a cited directory entry. Entries are canonical (cleaned, no
// trailing slash), so a directory entry matches everything beneath itself.
func isCitedAttachment(rel string, cited map[string]struct{}) bool {
	for ref := range cited {
		if rel == ref || strings.HasPrefix(rel, ref+"/") {
			return true
		}
	}
	return false
}

// extractSkillRefs returns the references/, assets/, scripts/ paths cited in
// a markdown body, de-duplicated in order of first appearance and normalized
// to canonical root-relative form (references/./guide.md and
// references/guide.md are the same citation). The body is parsed as
// CommonMark and citations are collected from prose text, from local link
// and image destinations, and from inline code spans that cite a path.
// Fenced and indented code blocks, remote URLs, autolinks, and raw HTML
// never yield references — they are documentation examples or external
// resources, not skill attachments.
func extractSkillRefs(body []byte) []string {
	doc := goldmark.New().Parser().Parse(text.NewReader(body))
	seen := make(map[string]struct{})
	var refs []string
	add := func(ref string) {
		norm, ok := normalizeSkillRef(ref)
		if !ok {
			return
		}
		if _, ok := seen[norm]; !ok {
			seen[norm] = struct{}{}
			refs = append(refs, norm)
		}
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Text:
			collectProseRefs(node.Segment.Value(body), add)
		case *ast.Link:
			if ref, ok := localLinkTarget(node.Destination); ok {
				add(ref)
			}
		case *ast.Image:
			if ref, ok := localLinkTarget(node.Destination); ok {
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

// collectProseRefs collects root-anchored attachment citations from a prose
// text segment. Complete URL tokens are blanked out first so a query string
// or fragment (view?file=assets/x.png) never yields a phantom citation.
func collectProseRefs(prose []byte, add func(string)) {
	scrubbed := urlTokenPattern.ReplaceAll(prose, []byte(" "))
	for _, m := range skillRefPattern.FindAllSubmatch(scrubbed, -1) {
		add(string(m[1]))
	}
}

// normalizeSkillRef converts a SKILL.md attachment citation into its
// canonical root-relative form: a leading ./ is dropped and the path is
// cleaned, so equivalent spellings (references/./guide.md,
// references//guide.md) compare equal against filesystem-derived paths. ok
// is false when the citation escapes the attachment directories
// (references/../x) or does not point into references/, assets/, scripts/ at
// all. A citation naming a bare directory root (references, references/) is
// kept as a directory citation.
func normalizeSkillRef(ref string) (string, bool) {
	ref = strings.TrimPrefix(ref, "./")
	if ref == "" {
		return "", false
	}
	cleaned := path.Clean(ref)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	if hasRefPrefix(cleaned) {
		return cleaned, true
	}
	for _, prefix := range skillRefPrefixes {
		if cleaned == strings.TrimSuffix(prefix, "/") {
			return cleaned, true
		}
	}
	return "", false
}

// localLinkTarget reduces a link or image destination to a local path
// candidate: the fragment and query are stripped, and remote URLs, pure
// anchors, and protocol-relative destinations are rejected.
func localLinkTarget(dest []byte) (string, bool) {
	s := string(dest)
	if i := strings.IndexAny(s, "#?"); i >= 0 {
		s = s[:i]
	}
	if s == "" || strings.HasPrefix(s, "//") || linkSchemePattern.MatchString(s) {
		return "", false
	}
	return s, true
}

// citesRefPath reports whether the first whitespace-separated token of s
// starts with one of the attachment directory prefixes.
func citesRefPath(s string) bool {
	if fields := strings.Fields(s); len(fields) > 0 {
		return hasRefPrefix(strings.TrimPrefix(fields[0], "./"))
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
