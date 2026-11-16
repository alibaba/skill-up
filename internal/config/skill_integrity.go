package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"gopkg.in/yaml.v3"
)

// refNamePattern matches the part of a path mentioned in prose that follows
// its leading directory. Prose has no delimiters, so the path is read
// heuristically: it starts on a letter, digit, or underscore and may contain
// letters and digits of any script, combining marks, _ - . / @ + ~ %, and
// balanced parentheses (assets/icon@2x.png, references/指南.md,
// assets/diagram(1).png). Whitespace ends it. It ends on an ASCII letter,
// digit, or underscore, or on a closing parenthesis, so trailing sentence
// punctuation (.,;:!?。) and closing brackets are never captured, and CJK
// prose that directly follows a path (references/guide.md获取详情) is left
// out.
const refNamePattern = `(?:[\pL\pN_](?:[\pL\pN\pM_@+~%./-]|\([^()\s]*\))*(?:\w|\([^()\s]*\))|\w)`

// skillRefPattern matches references/, assets/, scripts/ paths cited in
// markdown prose — the conventional Agent Skill attachment directories. A
// match must start at a boundary (the beginning of a text segment, after a
// character that cannot be part of a URL, a home-dir path, or a longer path,
// or after an explicit ./ prefix); refNamePattern describes the rest.
// Group 1 is the path.
var skillRefPattern = regexp.MustCompile(`(?:^|[^\w./~-]|\./)((?:references|assets|scripts)/` + refNamePattern + `)`)

// skillRefPrefixes are the attachment directories a citation must point into.
var skillRefPrefixes = []string{"references/", "assets/", "scripts/"}

// linkSchemePattern matches the scheme prefix of a URI (https:, mailto:, ...).
var linkSchemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// urlTokenPattern matches a complete URL token — scheme://... or a bare
// www. host — so prose scanning can blank it out before collecting
// citations: a query string or fragment may contain an attachment-looking
// path (view?file=assets/x.png) that is part of the URL, not a skill
// citation. The token is limited to ASCII URL characters, so it ends at
// whitespace and at non-ASCII prose such as a full-width comma
// (详见https://example.com/docs，以及references/guide.md).
var urlTokenPattern = regexp.MustCompile(`(?:[a-zA-Z][a-zA-Z0-9+.-]*://|\bwww\.)[A-Za-z0-9_.~:/?#\[\]@!$&'()*+,;=%-]+`)

// relScriptPattern matches ./-prefixed relative path mentions inside script
// and data files (`source ./lib.sh`). Group 1 is the path relative to the
// mentioning file's own directory.
var relScriptPattern = regexp.MustCompile(`(?:^|[^\w./~-])\./(` + refNamePattern + `)`)

// CheckSkillIntegrity inspects the SKILL.md of the skill rooted at skillDir
// and returns one human-readable warning per finding: a missing or
// unterminated YAML frontmatter block, an empty name or description field,
// and references/, assets/, scripts/ paths cited in the markdown body that do
// not exist on disk. The body is parsed as CommonMark, so only real citations
// are collected — prose text, local link and image destinations, and inline
// code spans that cite a path. Code blocks, remote URLs, autolinks, and raw
// HTML never yield references — they are documentation examples or external
// resources, not skill attachments.
//
// It returns nil when skillDir has no SKILL.md (the directory is not a skill
// root, so there is nothing to check). Every finding is a warning; callers
// decide whether to tolerate or enforce them (skill-up validate --skill
// --strict). Attachment files the body never reaches are a separate,
// heuristic policy: see CheckSkillUncitedAttachments.
func CheckSkillIntegrity(skillDir string) []string {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("SKILL.md: unreadable: %v", err)}
	}

	body, warnings := splitSkillMD(data)
	exists := skillPathExists(skillDir)
	for _, ref := range missingSkillRefs(extractSkillRefs(body, exists), exists) {
		warnings = append(warnings, fmt.Sprintf("SKILL.md: references %q but it does not exist on disk", ref))
	}
	return warnings
}

// CheckSkillUncitedAttachments returns one warning per file under the
// skill's references/, assets/, scripts/ directories that is not reachable
// from the SKILL.md body. Truncating the tail of SKILL.md silently turns the
// attachments its lost sections cited into such orphans.
//
// Reachability is transitive — an attachment a cited markdown document links
// to, or one a cited script mentions by a root-anchored or ./-relative path,
// counts as cited — but it remains a heuristic: a file a script loads through
// a computed path ($(dirname "$0")/lib.sh, a Python import) is reported even
// though the skill works. That is why this is an opt-in policy (skill-up
// validate --skill --uncited) rather than part of CheckSkillIntegrity. It
// returns nil when SKILL.md is missing or unreadable; CheckSkillIntegrity
// reports those.
func CheckSkillUncitedAttachments(skillDir string) []string {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return nil
	}

	body, _ := splitSkillMD(data)
	var warnings []string
	seeds := extractSkillRefs(body, skillPathExists(skillDir))
	for _, file := range uncitedSkillFiles(skillDir, collectReachableRefs(skillDir, seeds)) {
		warnings = append(warnings, fmt.Sprintf("SKILL.md: never references %q but the file exists on disk", file))
	}
	return warnings
}

// splitSkillMD returns the markdown body of SKILL.md content — everything
// after a closed frontmatter block, or the whole file when the frontmatter is
// missing or unterminated — together with the frontmatter findings.
func splitSkillMD(data []byte) (body []byte, warnings []string) {
	lines := bytes.Split(data, []byte("\n"))
	frontmatter, rest, closed := splitFrontmatter(lines)
	switch {
	case len(lines) == 0 || !isFence(lines[0]):
		return data, []string{"SKILL.md: YAML frontmatter is missing (file must start with a --- fence)"}
	case !closed:
		return data, []string{"SKILL.md: YAML frontmatter is not closed (missing closing --- fence)"}
	default:
		return rest, frontmatterFieldWarnings(frontmatter)
	}
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
// cited from the SKILL.md body that do not exist on disk. Only the body feeds
// this check: it is the document the skill author maintains, so its dangling
// citations are worth reporting, while attachments' own dependencies only
// feed the reachability set.
func missingSkillRefs(refs []string, exists func(string) bool) []string {
	var missing []string
	for _, ref := range refs {
		if !exists(ref) {
			missing = append(missing, ref)
		}
	}
	slices.Sort(missing)
	return missing
}

// skillPathExists returns a predicate reporting whether a canonical
// root-relative path (references/guide.md) exists under skillDir.
func skillPathExists(skillDir string) func(string) bool {
	return func(rel string) bool {
		_, err := os.Stat(filepath.Join(skillDir, filepath.FromSlash(rel)))
		return err == nil
	}
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
		// The citation alone keeps rel out of the orphan report — even when
		// it dangles (a broken symlink still shows up in the attachment walk,
		// and the missing check already covers the dangling side).
		cited[rel] = struct{}{}
		abs := filepath.Join(skillDir, filepath.FromSlash(rel))
		// #nosec G703 -- callers confine rel to the skill root (normalizeSkillRef
		// rejects ../ escapes); reads during traversal are additionally
		// symlink-contained by readSkillLocalFile.
		info, err := os.Stat(abs)
		if err != nil {
			return // dangling citation: reported by the missing check; nothing to traverse
		}
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
		walkMarkdownAttachmentRefs(rel, data, skillPathExists(skillDir), add)
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
// exists reports whether a root-relative path exists; it settles where a
// code span's file name ends (see collectCodeSpanRefs).
func walkMarkdownAttachmentRefs(rel string, data []byte, exists func(string) bool, add func(string)) {
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
			if !continuesProseRun(node) && !inLocalLinkLabel(node) {
				collectProseRefs(proseRun(node, data), add)
			}
		case *ast.Link:
			addLinkTarget(node.Destination)
		case *ast.Image:
			addLinkTarget(node.Destination)
		case *ast.CodeSpan:
			// Same convention as the SKILL.md body: a code span whose first
			// token is a path cites that path.
			if !inLocalLinkLabel(node) {
				collectCodeSpanRefs(codeSpanText(node, data), exists, add)
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
}

// readSkillLocalFile reads file only when it resolves inside the skill root,
// so a symlinked attachment cannot leak the dependency scan into the wider
// filesystem. Both paths are made absolute before their symlinks are
// resolved: for a relative skill root such as "." (skill-up validate --skill
// .), EvalSymlinks keeps the root as "." while the attachment resolves to
// "references/guide.md", which never starts with "./", so every attachment
// would be rejected and its dependencies never followed.
func readSkillLocalFile(skillDir, file string) ([]byte, bool) {
	root, err := resolvedAbsPath(skillDir)
	if err != nil {
		return nil, false
	}
	resolved, err := resolvedAbsPath(file)
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

// resolvedAbsPath returns p as an absolute path with every symlink resolved,
// so two locations on disk can be compared by prefix.
func resolvedAbsPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
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
// resources, not skill attachments. The label of a link to a local file is
// display text, not a citation of its own (see inLocalLinkLabel).
//
// File names keep every character their syntax allows: a link destination
// is resolved the way CommonMark renders it (see localLinkTarget), and a
// code span is read literally, with exists — whether a root-relative path
// exists — telling a file name that contains spaces from a command line (see
// collectCodeSpanRefs). Only prose, which has no delimiters, is read
// heuristically (see refNamePattern).
func extractSkillRefs(body []byte, exists func(string) bool) []string {
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
			if !continuesProseRun(node) && !inLocalLinkLabel(node) {
				collectProseRefs(proseRun(node, body), add)
			}
		case *ast.Link:
			if ref, ok := localLinkTarget(node.Destination); ok {
				add(ref)
			}
		case *ast.Image:
			if ref, ok := localLinkTarget(node.Destination); ok {
				add(ref)
			}
		case *ast.CodeSpan:
			if !inLocalLinkLabel(node) {
				collectCodeSpanRefs(codeSpanText(node, body), exists, add)
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// collectCodeSpanRefs collects the attachment paths an inline code span
// cites. A span whose first token is an attachment path cites it
// (`references/install.md`); a span that merely contains a path inside a
// command (`cat scripts/x.sh`) is an example and cites nothing.
//
// CommonMark keeps code span content literal — no backslash escapes, entity
// references, or percent-decoding — so the span names a file verbatim,
// whatever characters it contains (`assets/icon@2x.png`,
// `references/指南.md`). Whitespace is ambiguous, though: it may be part of a
// file name (`references/my guide.md`) or separate a command from its
// arguments (`scripts/run.sh --verbose`). So the whole span, and then each
// whitespace-separated token, is cited verbatim when it names an existing
// path; anything else is read like prose, which keeps a missing citation
// reportable and drops what cannot end a path (`scripts/run.sh;`,
// `references/guide.md#setup`).
func collectCodeSpanRefs(content string, exists func(string) bool, add func(string)) {
	if !citesRefPath(content) {
		return
	}
	if citeExistingPath(strings.TrimSpace(content), exists, add) {
		return
	}
	for token := range strings.FieldsSeq(content) {
		if !citeExistingPath(token, exists, add) {
			collectProseRefs([]byte(token), add)
		}
	}
}

// citeExistingPath cites s when it names an existing path inside one of the
// attachment directories and reports whether it did. A bare attachment root
// (`references/`) mentions the convention rather than citing anything.
func citeExistingPath(s string, exists func(string) bool, add func(string)) bool {
	norm, ok := normalizeSkillRef(s)
	if !ok || !hasRefPrefix(norm) || !exists(norm) {
		return false
	}
	add(norm)
	return true
}

// inLocalLinkLabel reports whether n sits in the label of a link, or the alt
// text of an image, whose destination is local. Such a link cites its
// destination; the label is display text that often spells the same path
// for the reader ([references/my guide.md](<references/my guide.md>)), and
// reading it as prose would cut the file name at its first space. The label
// of a remote link is still read as prose.
func inLocalLinkLabel(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		var dest []byte
		switch link := p.(type) {
		case *ast.Link:
			dest = link.Destination
		case *ast.Image:
			dest = link.Destination
		default:
			continue
		}
		_, local := localLinkTarget(dest)
		return local
	}
	return false
}

// proseRun returns the text of first and of the text siblings that directly
// follow it as one string. goldmark ends a text node after an emphasis
// delimiter, so an underscore inside a path (references/api_docs.md) or a URL
// (https://example.com/a_b?f=assets/x.png) splits that token across adjacent
// text nodes; scanning the run as a whole keeps it intact. Line breaks between
// the nodes are kept, and backslash escapes are resolved the way CommonMark
// renders them (references/api\_docs.md cites references/api_docs.md).
func proseRun(first *ast.Text, src []byte) []byte {
	var buf bytes.Buffer
	for n := ast.Node(first); n != nil; n = n.NextSibling() {
		t, ok := n.(*ast.Text)
		if !ok {
			break
		}
		buf.Write(t.Segment.Value(src))
		if t.SoftLineBreak() || t.HardLineBreak() {
			buf.WriteByte('\n')
		}
	}
	return util.UnescapePunctuations(buf.Bytes())
}

// continuesProseRun reports whether t was already scanned as part of the
// prose run that starts at an earlier text sibling (see proseRun).
func continuesProseRun(t *ast.Text) bool {
	_, ok := t.PreviousSibling().(*ast.Text)
	return ok
}

// collectProseRefs collects root-anchored attachment citations from a prose
// text segment. Complete URL tokens are blanked out first so a query string
// or fragment (view?file=assets/x.png) never yields a phantom citation.
func collectProseRefs(prose []byte, add func(string)) {
	collectPatternRefs(urlTokenPattern.ReplaceAll(prose, []byte(" ")), add)
}

// collectPatternRefs collects the skillRefPattern matches in prose. CJK prose
// often omits the spaces around a path, so a match may run on into the text
// that follows it (references/guide.md和assets/x.png); such a match is cut
// where gluedProseIndex places the end of the path, and the rest is scanned
// again.
func collectPatternRefs(prose []byte, add func(string)) {
	for _, m := range skillRefPattern.FindAllSubmatch(prose, -1) {
		ref := m[1]
		cut := gluedProseIndex(ref)
		if cut < 0 {
			add(string(ref))
			continue
		}
		// What is left must still end like a path (references/指南和... cut
		// back to references/ is no citation).
		if last := ref[cut-1]; last == ')' || last == '_' || isASCIIAlnum(rune(last)) {
			add(string(ref[:cut]))
		}
		collectPatternRefs(ref[cut:], add)
	}
}

// gluedProseIndex returns the byte offset where a prose path ends when CJK
// prose directly follows it, or -1 when the whole match is the path. The path
// ends before a non-ASCII run that directly follows a file extension — a dot,
// an ASCII letter, then ASCII letters or digits (references/guide.md和b.md) —
// or that is directly followed by the next attachment path
// (references/a和references/b). Anywhere else a non-ASCII run belongs to the
// file name (references/指南.md, references/v1.0版本说明.md).
func gluedProseIndex(ref []byte) int {
	s := string(ref)
	afterDot, inExt := false, false
	runStart := -1 // start of the current non-ASCII run
	for i, r := range s {
		if r >= utf8.RuneSelf {
			if inExt {
				return i
			}
			if runStart < 0 {
				runStart = i
			}
			afterDot = false
			continue
		}
		if runStart >= 0 {
			if hasRefPrefix(s[i:]) {
				return runStart
			}
			runStart = -1
		}
		switch {
		case r == '.':
			afterDot, inExt = true, false
		case afterDot && isASCIILetter(r):
			afterDot, inExt = false, true
		case inExt && isASCIIAlnum(r):
			// still inside the extension
		default:
			afterDot, inExt = false, false
		}
	}
	return -1
}

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

func isASCIIAlnum(r rune) bool { return isASCIILetter(r) || r >= '0' && r <= '9' }

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
// candidate. goldmark keeps the destination as written, so it is first
// resolved the way CommonMark renders it — backslash escapes, then numeric
// and named character references (assets/diagram\(1\).png names
// assets/diagram(1).png) — which gives the URL the link points to. Then the
// fragment and query are stripped, percent-encoding is decoded the way a
// renderer resolves the link (references/my%20guide.md names
// references/my guide.md), and remote URLs, pure anchors, and
// protocol-relative destinations are rejected. A destination that is not
// valid percent-encoding (references/100%.md) is kept verbatim.
func localLinkTarget(dest []byte) (string, bool) {
	s := string(util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(dest))))
	if i := strings.IndexAny(s, "#?"); i >= 0 {
		s = s[:i]
	}
	if s == "" || strings.HasPrefix(s, "//") || linkSchemePattern.MatchString(s) {
		return "", false
	}
	if decoded, err := url.PathUnescape(s); err == nil {
		s = decoded
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

// codeSpanText returns the content of a code span the way CommonMark renders
// it: the text of its children, with each line ending turned into a space.
func codeSpanText(span *ast.CodeSpan, src []byte) string {
	var buf bytes.Buffer
	for child := span.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			v := t.Segment.Value(src)
			if line, ok := bytes.CutSuffix(v, []byte("\n")); ok {
				buf.Write(line)
				buf.WriteByte(' ')
				continue
			}
			buf.Write(v)
		}
	}
	return buf.String()
}
