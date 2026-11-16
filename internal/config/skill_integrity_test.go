package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCheckSkillIntegrity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		dir          string
		wantWarnings []string // substrings, in order; empty means no warnings
	}{
		{
			name:         "healthy skill produces no warnings",
			dir:          "testdata/skill-integrity/healthy",
			wantWarnings: nil,
		},
		{
			name: "empty frontmatter reports missing name and description",
			dir:  "testdata/skill-integrity/empty-frontmatter",
			wantWarnings: []string{
				"frontmatter field 'name' is missing or empty",
				"frontmatter field 'description' is missing or empty",
			},
		},
		{
			name: "dangling attachment paths are reported in sorted order",
			dir:  "testdata/skill-integrity/broken-references",
			wantWarnings: []string{
				`references "assets/gone.png" but it does not exist on disk`,
				`references "references/missing.md" but it does not exist on disk`,
			},
		},
		{
			name:         "paths inside fenced code blocks are not references",
			dir:          "testdata/skill-integrity/codeblock-refs",
			wantWarnings: nil,
		},
		{
			name:         "sentence punctuation, remote URLs, and tilde fences are not references",
			dir:          "testdata/skill-integrity/markdown-edges",
			wantWarnings: nil,
		},
		{
			name: "missing frontmatter is reported and body references are still checked",
			dir:  "testdata/skill-integrity/missing-frontmatter",
			wantWarnings: []string{
				"YAML frontmatter is missing",
				`references "references/orphan.md" but it does not exist on disk`,
			},
		},
		{
			name: "unterminated frontmatter is reported and body references are still checked",
			dir:  "testdata/skill-integrity/unterminated-frontmatter",
			wantWarnings: []string{
				"YAML frontmatter is not closed",
				`references "references/anything.md" but it does not exist on disk`,
			},
		},
		{
			name:         "directory without SKILL.md is skipped",
			dir:          "testdata/skill-integrity",
			wantWarnings: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := CheckSkillIntegrity(tt.dir)
			if len(got) != len(tt.wantWarnings) {
				t.Fatalf("CheckSkillIntegrity(%q) = %v, want %d warning(s)", tt.dir, got, len(tt.wantWarnings))
			}
			for i, want := range tt.wantWarnings {
				if !strings.Contains(got[i], want) {
					t.Errorf("warning[%d] = %q, want substring %q", i, got[i], want)
				}
			}
		})
	}
}

func TestCheckSkillUncitedAttachmentsFixture(t *testing.T) {
	t.Parallel()

	// The fixture cites references/guide.md only; every other attachment
	// file must be reported in sorted order, except the .gitkeep dotfile.
	got := CheckSkillUncitedAttachments("testdata/skill-integrity/orphan-attachment")
	want := []string{
		`never references "assets/unused-diagram.png" but the file exists on disk`,
		`never references "references/uncited-notes.md" but the file exists on disk`,
	}
	if len(got) != len(want) {
		t.Fatalf("CheckSkillUncitedAttachments() = %v, want %d warning(s): %v", got, len(want), want)
	}
	for i, wantSub := range want {
		if !strings.Contains(got[i], wantSub) {
			t.Errorf("warning[%d] = %q, want substring %q", i, got[i], wantSub)
		}
	}
}

func TestCheckSkillIntegrityLeavesUncitedFilesToOptInCheck(t *testing.T) {
	t.Parallel()

	// The default check stays deterministic: uncited attachments — including
	// a helper a cited script loads through a computed path, which the
	// reachability heuristic cannot follow — are left to the opt-in
	// CheckSkillUncitedAttachments policy.
	if got := CheckSkillIntegrity("testdata/skill-integrity/orphan-attachment"); len(got) != 0 {
		t.Fatalf("CheckSkillIntegrity(orphan-attachment) = %v, want no warnings", got)
	}

	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: computed-path
description: Skill whose entrypoint script sources a sibling through a computed path.
---

Run ` + "`scripts/run.sh`" + ` to execute.
`,
		"scripts/run.sh": "#!/bin/sh\n. \"$(dirname \"$0\")/lib.sh\"\n",
		"scripts/lib.sh": "# lib\n",
	})
	if got := CheckSkillIntegrity(dir); len(got) != 0 {
		t.Fatalf("CheckSkillIntegrity() = %v, want no warnings for a computed-path script dependency", got)
	}
}

func TestCheckSkillUncitedAttachmentsSkipsNonSkillDirectory(t *testing.T) {
	t.Parallel()

	if got := CheckSkillUncitedAttachments("testdata/skill-integrity"); got != nil {
		t.Fatalf("CheckSkillUncitedAttachments() = %v, want nil for a directory without SKILL.md", got)
	}
}

func TestCheckSkillIntegrityMarkdownEdgesWithMissingRef(t *testing.T) {
	t.Parallel()

	// The tricky constructs stay silent, but a genuinely missing local
	// reference must still be reported.
	dir := t.TempDir()
	skillMD := `---
name: tricky-skill
description: Tricky but valid markdown plus one genuinely missing reference.
---

See references/guide.md. Mirror: [icon](https://example.com/assets/icon.png)

~~~sh
cat scripts/example.sh
~~~

Details live in references/missing.md.
`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "guide.md"), []byte("# Guide\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := CheckSkillIntegrity(dir)
	if len(got) != 1 || !strings.Contains(got[0], `"references/missing.md"`) {
		t.Fatalf("CheckSkillIntegrity() = %v, want exactly one warning for references/missing.md", got)
	}
}

func TestCheckSkillUncitedAttachmentsOrphanEdges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	skillMD := `---
name: orphan-edges
description: Exercises the uncited-attachment corner cases.
---

Browse [the docs](references/advanced) or run ` + "`scripts/main.sh`" + `.
The icon lives at ./assets/slashy.png.
`
	files := map[string]string{
		"SKILL.md":                       skillMD,
		"references/advanced/covered.md": "# Covered by the cited directory\n",
		"references/lonely.md":           "# Uncited top-level file\n",
		"references/.hidden/skipped.md":  "# Inside a dot directory\n",
		"assets/slashy.png":              "cited with a ./ prefix\n",
		"assets/sub/nested.png":          "uncited nested asset\n",
		"assets/other.bin":               "uncited flat asset\n",
		"assets/.DS_Store":               "metadata\n",
		"scripts/main.sh":                "echo cited\n",
	}
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got := CheckSkillUncitedAttachments(dir)
	want := []string{
		`never references "assets/other.bin" but the file exists on disk`,
		`never references "assets/sub/nested.png" but the file exists on disk`,
		`never references "references/lonely.md" but the file exists on disk`,
	}
	if len(got) != len(want) {
		t.Fatalf("CheckSkillUncitedAttachments() = %v, want %d warning(s): %v", got, len(want), want)
	}
	for i, wantSub := range want {
		if !strings.Contains(got[i], wantSub) {
			t.Errorf("warning[%d] = %q, want substring %q", i, got[i], wantSub)
		}
	}
}

// allSkillWarnings runs the integrity check and the opt-in uncited-attachment
// check together, as skill-up validate --skill --uncited does.
func allSkillWarnings(dir string) []string {
	return append(CheckSkillIntegrity(dir), CheckSkillUncitedAttachments(dir)...)
}

// writeSkillFiles materializes a skill directory from a map of
// slash-separated relative paths to file contents.
func writeSkillFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckSkillIntegrityIndirectReferenceChain(t *testing.T) {
	t.Parallel()

	// Regression: a file reachable through a cited attachment counts as
	// cited, even though SKILL.md never names it directly.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: indirect-chain
description: Skill whose SKILL.md reaches an attachment through another attachment.
---

Read [the guide](references/guide.md).
`,
		"references/guide.md":   "# Guide\n\nRead [details](details.md) when needed.\n",
		"references/details.md": "# Details\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for a valid indirect reference chain", got)
	}
}

func TestCheckSkillIntegrityScriptDependencies(t *testing.T) {
	t.Parallel()

	// A cited script reaches its helpers: ./-relative mentions resolve
	// against the script's own directory, root-anchored mentions against the
	// skill root.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: script-deps
description: Skill whose entrypoint script sources sibling helpers.
---

Run ` + "`scripts/run.sh`" + ` to execute.
`,
		"scripts/run.sh":    "#!/bin/sh\n. ./lib.sh\nscripts/helper.sh --flag\n",
		"scripts/lib.sh":    "# lib\n",
		"scripts/helper.sh": "# helper\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for script dependencies", got)
	}
}

func TestCheckSkillIntegrityCitedDirectoryDependencies(t *testing.T) {
	t.Parallel()

	// A directory citation covers the documents beneath it, and their own
	// dependencies are reachable too.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: dir-deps
description: Skill citing a whole documentation directory.
---

Browse [the docs](references/).
`,
		"references/guide.md": "# Guide\n\nSee ![the diagram](../assets/diagram.png).\n",
		"assets/diagram.png":  "not really a png\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for directory citation dependencies", got)
	}
}

func TestCheckSkillIntegrityCyclicReferencesTerminate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: cyclic
description: Skill whose documents link to each other.
---

Read [a](references/a.md).
`,
		"references/a.md": "# A\n\nContinue to [b](b.md).\n",
		"references/b.md": "# B\n\nBack to [a](a.md).\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for a cyclic reference chain", got)
	}
}

func TestCheckSkillIntegrityUnreachableDocDoesNotRescue(t *testing.T) {
	t.Parallel()

	// Reachability starts at the SKILL.md body: a document nothing cites
	// stays an orphan, and so does the file only it links to.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: unreachable
description: Skill with a documentation island.
---

This skill cites no attachments at all.
`,
		"references/guide.md":   "# Guide\n\nRead [details](details.md).\n",
		"references/details.md": "# Details\n",
	})

	got := allSkillWarnings(dir)
	want := []string{
		`never references "references/details.md" but the file exists on disk`,
		`never references "references/guide.md" but the file exists on disk`,
	}
	if len(got) != len(want) {
		t.Fatalf("allSkillWarnings() = %v, want %d warning(s): %v", got, len(want), want)
	}
	for i, wantSub := range want {
		if !strings.Contains(got[i], wantSub) {
			t.Errorf("warning[%d] = %q, want substring %q", i, got[i], wantSub)
		}
	}
}

func TestCheckSkillIntegrityBrokenSymlinkCitation(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}

	// A citation whose target is a broken symlink is reported once — as a
	// missing reference — and must not additionally surface as an uncited
	// attachment (the symlink entry itself still appears in the walk).
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: broken-symlink
description: Skill citing a dangling symlink.
---

Read [the doc](references/gone.md).
`,
	})
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent/target", filepath.Join(dir, "references", "gone.md")); err != nil {
		t.Fatal(err)
	}

	got := allSkillWarnings(dir)
	if len(got) != 1 || !strings.Contains(got[0], `references "references/gone.md" but it does not exist on disk`) {
		t.Fatalf("allSkillWarnings() = %v, want exactly one missing-reference warning for references/gone.md", got)
	}
}

func TestCheckSkillIntegrityEquivalentLocalPaths(t *testing.T) {
	t.Parallel()

	// Regression: equivalent spellings of one path must normalize to the
	// same citation for both the existence check and the cited comparison.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: equivalent-paths
description: Skill citing one file through equivalent path spellings.
---

Read [a](references/./guide.md), [b](references//notes.md), and
[c](references/sub/../deep.md).
`,
		"references/guide.md": "# Guide\n",
		"references/notes.md": "# Notes\n",
		"references/deep.md":  "# Deep\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for equivalent local paths", got)
	}
}

func TestCheckSkillIntegrityRemoteURLQueryAndFragment(t *testing.T) {
	t.Parallel()

	// Regression: a path inside a remote URL's query string or fragment is
	// part of the URL, not a local citation.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: remote-urls
description: Skill whose prose only mentions remote URLs.
---

Remote: https://example.com/view?file=assets/missing.png
Mirror: https://example.com/page#assets/icon.png
`,
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for remote URL query/fragment text", got)
	}
}

func TestCheckSkillIntegrityUnderscoredCitations(t *testing.T) {
	t.Parallel()

	// Regression: an underscore splits goldmark's prose text nodes, which used
	// to truncate references/api_docs.md to a phantom "references/api_"
	// citation and leave the real file uncited.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: underscored
description: Skill citing snake_case attachments in prose and link text.
---

See references/api_docs.md for the API, and read
[references/error_codes.md](references/error_codes.md) or references/rate\_limits.md.
`,
		"references/api_docs.md":    "# API\n",
		"references/error_codes.md": "# Errors\n",
		"references/rate_limits.md": "# Limits\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for underscored citations", got)
	}
}

func TestCheckSkillIntegrityPercentEncodedLinks(t *testing.T) {
	t.Parallel()

	// Regression: a percent-encoded link destination names the decoded file,
	// both when SKILL.md cites it and when a cited document links onward
	// relative to itself.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: percent-encoded
description: Skill linking to attachments whose names contain spaces.
---

Read [the guide](references/my%20guide.md).
`,
		"references/my guide.md":   "# Guide\n\nThen read [more](more%20notes.md).\n",
		"references/more notes.md": "# More\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for percent-encoded links", got)
	}
}

func TestCheckSkillIntegrityCitedBinaryIsNotScanned(t *testing.T) {
	t.Parallel()

	// A cited binary is never scanned for dependencies: the attachment-path
	// bytes inside it must not rescue an otherwise unreachable file.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: binary-cited
description: Skill citing a binary asset.
---

The logo is ![logo](assets/logo.png).
`,
		"references/ghost.md": "# Unreachable\n",
	})
	binary := append([]byte("\x89PNG\r\n\x1a\n\x00\x00"), []byte("references/ghost.md")...)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "logo.png"), binary, 0o600); err != nil {
		t.Fatal(err)
	}

	got := allSkillWarnings(dir)
	if len(got) != 1 || !strings.Contains(got[0], `"references/ghost.md"`) {
		t.Fatalf("allSkillWarnings() = %v, want exactly one orphan warning for references/ghost.md", got)
	}
}

func TestCheckSkillIntegritySymlinkedAttachmentStaysContained(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}

	// A symlinked attachment resolving outside the skill root is cited (it
	// exists) but never scanned: its link text must not rescue files inside
	// the skill, and the scan must not read outside files.
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.md")
	if err := os.WriteFile(outside, []byte("See [inside](references/inside.md).\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: symlinked
description: Skill citing a symlinked document.
---

Read [the doc](references/evil.md).
`,
		"references/inside.md": "# Uncited\n",
	})
	if err := os.Symlink(outside, filepath.Join(dir, "references", "evil.md")); err != nil {
		t.Fatal(err)
	}

	got := allSkillWarnings(dir)
	if len(got) != 1 || !strings.Contains(got[0], `"references/inside.md"`) {
		t.Fatalf("allSkillWarnings() = %v, want exactly one orphan warning for references/inside.md", got)
	}
}

func TestCheckSkillUncitedAttachmentsRelativeSkillRoot(t *testing.T) {
	// Not parallel: t.Chdir changes the working directory of the process.
	//
	// Regression (#281 review, 2026-11-16): with --skill . the containment
	// check compared the attachment "references/guide.md" against the root
	// prefix "./", rejected it, and never followed its link, so details.md
	// was reported as uncited while the absolute path worked.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: indirect-chain
description: Skill whose SKILL.md reaches an attachment through another attachment.
---

Read [the guide](references/guide.md).
`,
		"references/guide.md":   "# Guide\n\nRead [details](details.md) when needed.\n",
		"references/details.md": "# Details\n",
	})
	t.Chdir(dir)

	for _, root := range []string{".", "./", "references/.."} {
		if got := allSkillWarnings(root); len(got) != 0 {
			t.Errorf("allSkillWarnings(%q) = %v, want no warnings for a valid indirect reference chain", root, got)
		}
	}
}

func TestCheckSkillIntegrityInlineCodeAtSignFileName(t *testing.T) {
	t.Parallel()

	// Regression (#281 review, 2026-11-16): the inline-code citation
	// assets/icon@2x.png was cut at the @ and reported as a missing
	// "assets/icon". A code span is literal (CommonMark 0.31.2 §6.1), so it
	// names the file verbatim.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: retina-icon
description: Skill citing an asset whose name contains an at sign.
---

Use ` + "`assets/icon@2x.png`" + ` on high-DPI screens.
`,
		"assets/icon@2x.png": "not really a png\n",
	})
	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for an existing assets/icon@2x.png", got)
	}

	// Once the file is gone, the warning names the whole file.
	if err := os.Remove(filepath.Join(dir, "assets", "icon@2x.png")); err != nil {
		t.Fatal(err)
	}
	got := CheckSkillIntegrity(dir)
	if len(got) != 1 || !strings.Contains(got[0], `references "assets/icon@2x.png" but it does not exist on disk`) {
		t.Fatalf("CheckSkillIntegrity() = %v, want exactly one missing-reference warning for assets/icon@2x.png", got)
	}
}

func TestCheckSkillIntegrityNonASCIIFileNames(t *testing.T) {
	t.Parallel()

	// Every citation below is valid, and each must name the whole file:
	//   - a code span is literal (CommonMark 0.31.2 §6.1);
	//   - a bare link destination excludes only spaces, ASCII control
	//     characters, and unbalanced parentheses (§6.3), so non-ASCII
	//     characters may appear as they are, or percent-encoded as UTF-8;
	//   - prose has no delimiters, so a path there is read heuristically: its
	//     file name may hold letters of any script, and CJK text that follows
	//     a path without a space ends it after the extension.
	// The cited script mentions a non-ASCII sibling as ./工具.sh, which keeps
	// the --uncited reachability walk honest for script dependencies too.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: non-ascii
description: Skill citing attachments with non-ASCII file names.
---

Read ` + "`references/指南.md`" + ` first, then [the notes](references/笔记.md) and
[the FAQ](references/%E5%B8%B8%E8%A7%81%E9%97%AE%E9%A2%98.md).

参见 references/使用说明-v2.md。详见references/版本记录.md获取更多信息。

Run ` + "`scripts/run.sh`" + ` to build the report.
`,
		"references/指南.md":      "# 指南\n",
		"references/笔记.md":      "# 笔记\n",
		"references/常见问题.md":    "# 常见问题\n",
		"references/使用说明-v2.md": "# 使用说明\n",
		"references/版本记录.md":    "# 版本记录\n",
		"scripts/run.sh":        "#!/bin/sh\n. ./工具.sh\n",
		"scripts/工具.sh":         "# helpers\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for non-ASCII file names", got)
	}
}

func TestCheckSkillIntegritySpacesAndParenthesesInFileNames(t *testing.T) {
	t.Parallel()

	// The valid ways to cite references/my guide (v2).md and
	// assets/diagram(1).png, per CommonMark 0.31.2:
	//   - §6.3: a destination in pointy brackets may contain spaces and
	//     parentheses. The link cites its destination; its label is display
	//     text, so a label that spells the same path is not read as prose.
	//   - §6.3: a bare destination cannot contain a space, so spaces are
	//     percent-encoded; parentheses are allowed when balanced, or when
	//     backslash-escaped (backslash escapes work in URLs, §2.4).
	//   - §6.1: a code span is literal, spaces included. A span can also be a
	//     command line (`scripts/run.sh --verbose`), so the whole span counts
	//     as one file name when that file exists.
	dir := t.TempDir()
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: spaces-parens
description: Skill citing attachments whose names contain spaces and parentheses.
---

Read [references/my guide (v2).md](<references/my guide (v2).md>), also linked as
[the guide](references/my%20guide%20(v2).md), or open ` + "`references/my guide (v2).md`" + `.

![diagram](assets/diagram\(1\).png) is also [here](assets/diagram(1).png).
`,
		"references/my guide (v2).md": "# Guide\n",
		"assets/diagram(1).png":       "not really a png\n",
	})

	if got := allSkillWarnings(dir); len(got) != 0 {
		t.Fatalf("allSkillWarnings() = %v, want no warnings for the CommonMark-valid spellings", got)
	}

	// Not a valid spelling: a bare destination cannot contain a space
	// (§6.3), so CommonMark renders this as literal text rather than a link.
	// Read as prose, the citation ends at the first space and the broken
	// link is reported.
	writeSkillFiles(t, dir, map[string]string{
		"SKILL.md": `---
name: spaces-parens
description: Skill whose link destination contains an unencoded space.
---

Read [the guide](references/my guide (v2).md).
`,
	})
	got := CheckSkillIntegrity(dir)
	if len(got) != 1 || !strings.Contains(got[0], `references "references/my" but it does not exist on disk`) {
		t.Fatalf("CheckSkillIntegrity() = %v, want the unencoded space to end the citation", got)
	}
}

type extractRefsCase struct {
	name   string
	body   string
	onDisk []string // root-relative paths that exist; nil means none does
	want   []string // refs in order of first appearance; nil means none
}

func runExtractRefsCases(t *testing.T, tests []extractRefsCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exists := func(rel string) bool { return slices.Contains(tt.onDisk, rel) }
			got := extractSkillRefs([]byte(tt.body), exists)
			if len(got) != len(tt.want) {
				t.Fatalf("extractSkillRefs(%q) = %v, want %v", tt.body, got, tt.want)
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("ref[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestExtractSkillRefsProse(t *testing.T) {
	t.Parallel()

	runExtractRefsCases(t, []extractRefsCase{
		{
			name: "prose path with sentence-ending period",
			body: "See references/guide.md for details.",
			want: []string{"references/guide.md"},
		},
		{
			name: "prose path with CJK full stop",
			body: "参见 references/guide.md。",
			want: []string{"references/guide.md"},
		},
		{
			name: "prose path in parens and quotes",
			body: `("assets/icon.png")`,
			want: []string{"assets/icon.png"},
		},
		{
			name: "multiple prose paths keep order",
			body: "Read references/a.md, then run scripts/run.sh; see assets/t.txt.",
			want: []string{"references/a.md", "scripts/run.sh", "assets/t.txt"},
		},
		{
			name: "duplicate paths reported once",
			body: "references/a.md and references/a.md again",
			want: []string{"references/a.md"},
		},
		{
			name: "dot-slash prefixed prose path is a reference",
			body: "See ./assets/icon.png for the icon.",
			want: []string{"assets/icon.png"},
		},
		{
			name: "bare URL in prose is ignored",
			body: "Mirror at https://example.com/assets/icon.png for reference.",
			want: nil,
		},
		{
			name: "bare URL with a query string is ignored",
			body: "Remote: https://example.com/view?file=assets/missing.png",
			want: nil,
		},
		{
			name: "bare URL with a fragment is ignored",
			body: "See https://example.com/page#assets/icon.png for details.",
			want: nil,
		},
		{
			name: "citation after a bare URL is still found",
			body: "https://example.com/x?f=assets/a.png points at references/real.md",
			want: []string{"references/real.md"},
		},
		{
			name: "home-dir path in prose is ignored",
			body: "Also available at ~/scripts/run.sh locally.",
			want: nil,
		},
		{
			name: "paths inside blockquotes and lists are references",
			body: "> See references/guide.md.\n\n- Run scripts/run.sh.",
			want: []string{"references/guide.md", "scripts/run.sh"},
		},
		{
			name: "paths inside pipe tables are references",
			body: "| File | Purpose |\n| --- | --- |\n| references/guide.md | docs |",
			want: []string{"references/guide.md"},
		},
	})
}

// TestExtractSkillRefsProseRuns covers prose that goldmark splits across
// adjacent text nodes (an underscore ends a node), which must be scanned as
// one run so paths and URLs stay whole.
func TestExtractSkillRefsProseRuns(t *testing.T) {
	t.Parallel()

	runExtractRefsCases(t, []extractRefsCase{
		{
			name: "underscore inside a prose path keeps the path whole",
			body: "See references/api_docs.md for the API.",
			want: []string{"references/api_docs.md"},
		},
		{
			name: "underscore inside link text keeps the path whole",
			body: "Read [references/api_docs.md](references/api_docs.md) first.",
			want: []string{"references/api_docs.md"},
		},
		{
			name: "backslash-escaped underscore resolves to the rendered path",
			body: `See references/api\_docs.md for the API.`,
			want: []string{"references/api_docs.md"},
		},
		{
			name: "paths on consecutive lines stay separate",
			body: "First references/a.md\nreferences/b.md next.",
			want: []string{"references/a.md", "references/b.md"},
		},
		{
			name: "bare URL with an underscore in its path is ignored",
			body: "Remote: https://example.com/a_b/view?file=assets/missing.png",
			want: nil,
		},
		{
			name: "bare www URL with a query string is ignored",
			body: "Remote: www.example.com/view?file=assets/missing.png",
			want: nil,
		},
		{
			name: "underscored URL used as link text is ignored",
			body: "[https://example.com/a_b?file=assets/x.png](https://example.com/a_b?file=assets/x.png)",
			want: nil,
		},
		{
			name: "citation after a URL in CJK prose is found",
			body: "详见https://example.com/docs，以及references/guide.md。",
			want: []string{"references/guide.md"},
		},
	})
}

func TestExtractSkillRefsCodeConstructs(t *testing.T) {
	t.Parallel()

	runExtractRefsCases(t, []extractRefsCase{
		{
			name: "backtick-fenced code block is ignored",
			body: "```sh\ncat scripts/example.sh\n```",
			want: nil,
		},
		{
			name: "tilde-fenced code block is ignored",
			body: "~~~sh\ncat scripts/example.sh\n~~~",
			want: nil,
		},
		{
			name: "indented code block is ignored",
			body: "Intro paragraph.\n\n    cat scripts/example.sh\n",
			want: nil,
		},
		{
			name: "inline code span citing a path is a reference",
			body: "Read `references/install.md` first.",
			want: []string{"references/install.md"},
		},
		{
			name: "inline code span with a command is ignored",
			body: "Run `cat scripts/example.sh` to see it.",
			want: nil,
		},
		{
			name: "inline code span with path and arguments is a reference",
			body: "Run `scripts/run.sh --verbose` to execute.",
			want: []string{"scripts/run.sh"},
		},
		{
			name: "inline code span with dot-slash prefix is a reference",
			body: "Copy `./assets/template.txt` first.",
			want: []string{"assets/template.txt"},
		},
	})
}

func TestExtractSkillRefsLinks(t *testing.T) {
	t.Parallel()

	runExtractRefsCases(t, []extractRefsCase{
		{
			name: "remote link destination is ignored",
			body: "[icon](https://example.com/assets/icon.png)",
			want: nil,
		},
		{
			name: "remote image destination is ignored",
			body: "![icon](https://example.com/assets/icon.png)",
			want: nil,
		},
		{
			name: "local link destination is a reference",
			body: "[guide](references/guide.md)",
			want: []string{"references/guide.md"},
		},
		{
			name: "dot-slash prefixed link destination is a reference",
			body: "[icon](./assets/icon.png)",
			want: []string{"assets/icon.png"},
		},
		{
			name: "local link destination with anchor is a reference",
			body: "[guide](references/guide.md#setup)",
			want: []string{"references/guide.md"},
		},
		{
			name: "local image destination is a reference",
			body: "![icon](assets/icon.png)",
			want: []string{"assets/icon.png"},
		},
		{
			name: "reference-style link with local destination is a reference",
			body: "See [the guide][g].\n\n[g]: references/guide.md",
			want: []string{"references/guide.md"},
		},
		{
			name: "reference-style link with remote destination is ignored",
			body: "See [the icon][i].\n\n[i]: https://example.com/assets/icon.png",
			want: nil,
		},
		{
			name: "autolink is ignored",
			body: "<https://example.com/assets/icon.png>",
			want: nil,
		},
		{
			name: "raw HTML img src is ignored",
			body: `<img src="assets/icon.png" alt="icon">`,
			want: nil,
		},
		{
			name: "mailto link is ignored",
			body: "[mail](mailto:someone@example.com)",
			want: nil,
		},
	})
}

func TestExtractSkillRefsNormalization(t *testing.T) {
	t.Parallel()

	runExtractRefsCases(t, []extractRefsCase{
		{
			name: "prose path with a dot segment is normalized",
			body: "See references/a/./b.md for details.",
			want: []string{"references/a/b.md"},
		},
		{
			name: "link destination with a dot segment is normalized",
			body: "[guide](references/./guide.md)",
			want: []string{"references/guide.md"},
		},
		{
			name: "link destination with a double slash is normalized",
			body: "[guide](references//guide.md)",
			want: []string{"references/guide.md"},
		},
		{
			name: "link destination with a dot-dot segment is normalized",
			body: "[guide](references/sub/../guide.md)",
			want: []string{"references/guide.md"},
		},
		{
			name: "link destination escaping the attachment directories is ignored",
			body: "[secret](references/../secret.md)",
			want: nil,
		},
		{
			name: "bare directory link destination is a directory citation",
			body: "[docs](references/)",
			want: []string{"references"},
		},
		{
			name: "equivalent spellings collapse to one citation",
			body: "[a](references/./guide.md) and [b](references/guide.md)",
			want: []string{"references/guide.md"},
		},
		{
			name: "percent-encoded link destination is decoded",
			body: "[guide](references/my%20guide.md)",
			want: []string{"references/my guide.md"},
		},
		{
			name: "percent-encoded and plain spellings collapse to one citation",
			body: "[a](assets/logo%2Ddark.png) and ![b](assets/logo-dark.png)",
			want: []string{"assets/logo-dark.png"},
		},
		{
			name: "invalid percent escape is kept verbatim",
			body: "[odd](references/100%.md)",
			want: []string{"references/100%.md"},
		},
	})
}

// TestExtractSkillRefsFileNameProse covers file names with characters beyond
// [A-Za-z0-9_.-] in prose, which has no delimiters: the path is read
// heuristically (see refNamePattern and gluedProseIndex).
func TestExtractSkillRefsFileNameProse(t *testing.T) {
	t.Parallel()

	runExtractRefsCases(t, []extractRefsCase{
		{
			name: "at sign stays in the name",
			body: "See assets/icon@2x.png.",
			want: []string{"assets/icon@2x.png"},
		},
		{
			name: "balanced parentheses stay in the name",
			body: "See assets/diagram(1).png.",
			want: []string{"assets/diagram(1).png"},
		},
		{
			name: "closing parenthesis of the sentence is not part of the path",
			body: "(see references/guide.md)",
			want: []string{"references/guide.md"},
		},
		{
			name: "non-ASCII file name",
			body: "参见 references/使用说明-v2.md。",
			want: []string{"references/使用说明-v2.md"},
		},
		{
			name: "non-ASCII directory",
			body: "See references/中文/guide.md.",
			want: []string{"references/中文/guide.md"},
		},
		{
			name: "CJK prose after a non-ASCII file name is not part of it",
			body: "详见references/指南.md获取详情",
			want: []string{"references/指南.md"},
		},
		{
			name: "CJK prose after an ASCII file name is not part of it",
			body: "详见references/guide.md获取详情",
			want: []string{"references/guide.md"},
		},
		{
			name: "two paths joined by CJK prose stay separate",
			body: "参见references/a.md和assets/图.png",
			want: []string{"references/a.md", "assets/图.png"},
		},
		{
			name: "two extensionless paths joined by CJK prose stay separate",
			body: "参见references/advanced和references/basic目录",
			want: []string{"references/advanced", "references/basic"},
		},
		{
			name: "a version number before non-ASCII text stays in the name",
			body: "See references/v1.0版本说明.md.",
			want: []string{"references/v1.0版本说明.md"},
		},
		{
			// Without an extension, where the name ends in glued CJK prose is
			// undecidable; nothing is cited rather than a guess.
			name: "non-ASCII name without an extension in glued prose is not cited",
			body: "详见references/指南获取详情",
			want: nil,
		},
	})
}

// TestExtractSkillRefsFileNameCodeSpans covers file names in inline code.
// CommonMark keeps code span content literal (0.31.2 §6.1: no backslash
// escapes or entity references, line endings become spaces), so a span names
// a file verbatim. onDisk lists the files that exist: whitespace in a span is
// part of a file name only when that file exists, because a span can also be
// a command line.
func TestExtractSkillRefsFileNameCodeSpans(t *testing.T) {
	t.Parallel()

	runExtractRefsCases(t, []extractRefsCase{
		{
			name:   "at sign stays in the name",
			body:   "Use `assets/icon@2x.png` on high-DPI screens.",
			onDisk: []string{"assets/icon@2x.png"},
			want:   []string{"assets/icon@2x.png"},
		},
		{
			name: "missing file with an at sign is cited whole",
			body: "Use `assets/icon@2x.png` on high-DPI screens.",
			want: []string{"assets/icon@2x.png"},
		},
		{
			name: "non-ASCII file name",
			body: "Read `references/指南.md` first.",
			want: []string{"references/指南.md"},
		},
		{
			name:   "spaces and parentheses name an existing file",
			body:   "Open `references/my guide (v2).md` directly.",
			onDisk: []string{"references/my guide (v2).md"},
			want:   []string{"references/my guide (v2).md"},
		},
		{
			name: "without such a file the span reads as a command",
			body: "Open `references/my guide (v2).md` directly.",
			want: []string{"references/my"},
		},
		{
			name:   "a line ending inside the span is a space",
			body:   "Open `references/my\nguide.md` directly.",
			onDisk: []string{"references/my guide.md"},
			want:   []string{"references/my guide.md"},
		},
		{
			name:   "any character an existing file name holds is kept",
			body:   "Use `assets/a=b,c.png` and run `scripts/run=fast.sh --flag`.",
			onDisk: []string{"assets/a=b,c.png", "scripts/run=fast.sh"},
			want:   []string{"assets/a=b,c.png", "scripts/run=fast.sh"},
		},
		{
			name:   "an anchor is not part of the file name",
			body:   "See `references/guide.md#setup`.",
			onDisk: []string{"references/guide.md"},
			want:   []string{"references/guide.md"},
		},
		{
			name:   "shell punctuation is not part of the file name",
			body:   "Run `scripts/setup.sh; scripts/run.sh`.",
			onDisk: []string{"scripts/setup.sh", "scripts/run.sh"},
			want:   []string{"scripts/setup.sh", "scripts/run.sh"},
		},
		{
			name:   "a bare attachment directory is not a citation",
			body:   "Put documents in `references/`.",
			onDisk: []string{"references"},
			want:   nil,
		},
	})
}

// TestExtractSkillRefsFileNameLinks covers file names in link destinations,
// which CommonMark delimits exactly (0.31.2 §6.3): in pointy brackets a
// destination may contain spaces and parentheses; a bare destination cannot
// contain a space and allows parentheses only when balanced or
// backslash-escaped. Backslash escapes (§2.4) and entity references (§2.5)
// are resolved in destinations, as a renderer does before following them.
func TestExtractSkillRefsFileNameLinks(t *testing.T) {
	t.Parallel()

	runExtractRefsCases(t, []extractRefsCase{
		{
			name: "pointy-bracket destination keeps spaces and parentheses",
			body: "[guide](<references/my guide (v2).md>)",
			want: []string{"references/my guide (v2).md"},
		},
		{
			name: "percent-encoded spaces with balanced parentheses",
			body: "[guide](references/my%20guide%20(v2).md)",
			want: []string{"references/my guide (v2).md"},
		},
		{
			name: "backslash-escaped parentheses",
			body: `![diagram](assets/diagram\(1\).png)`,
			want: []string{"assets/diagram(1).png"},
		},
		{
			name: "named and numeric entity references",
			body: "[a](references/caf&eacute;.md) and [b](references/caf&#233;.md)",
			want: []string{"references/café.md"},
		},
		{
			name: "non-ASCII destination, raw or percent-encoded",
			body: "[a](references/指南.md) and [b](references/%E6%8C%87%E5%8D%97.md)",
			want: []string{"references/指南.md"},
		},
		{
			name: "at sign in a destination",
			body: "![icon](assets/icon@2x.png)",
			want: []string{"assets/icon@2x.png"},
		},
		{
			name: "label of a local link is display text, not a citation",
			body: "[references/my guide.md](<references/my guide.md>)",
			want: []string{"references/my guide.md"},
		},
		{
			name: "label of a remote link is still read as prose",
			body: "[see references/a.md](https://example.com/a)",
			want: []string{"references/a.md"},
		},
		{
			name: "image inside a remote link is still a citation",
			body: "[![badge](assets/badge.png)](https://ci.example.com)",
			want: []string{"assets/badge.png"},
		},
		{
			// Not a link: the space ends a bare destination (§6.3), so
			// CommonMark renders the line as literal text, read as prose.
			name: "unencoded space in a bare destination is not a link",
			body: "[guide](references/my guide.md)",
			want: []string{"references/my"},
		},
	})
}
