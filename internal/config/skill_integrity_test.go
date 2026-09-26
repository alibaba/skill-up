package config

import (
	"os"
	"path/filepath"
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
			name: "bare URL in prose is ignored",
			body: "Mirror at https://example.com/assets/icon.png for reference.",
			want: nil,
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
