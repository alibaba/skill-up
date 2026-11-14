package config

import (
	"os"
	"path/filepath"
	"runtime"
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

type extractRefsCase struct {
	name string
	body string
	want []string // refs in order of first appearance; nil means none
}

func runExtractRefsCases(t *testing.T, tests []extractRefsCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := extractSkillRefs([]byte(tt.body))
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
