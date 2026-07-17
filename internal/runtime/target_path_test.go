package runtime

import (
	"testing"

	"github.com/alibaba/skill-up/internal/platform"
)

func TestTargetPathPOSIX(t *testing.T) {
	t.Parallel()

	p := targetPathFor(platform.GOOSLinux)
	if got := p.clean(`/workspace/src/../result.json`); got != "/workspace/result.json" {
		t.Fatalf("clean = %q", got)
	}
	if got := p.dir(`/workspace/result.json`); got != "/workspace" {
		t.Fatalf("dir = %q", got)
	}
	if !p.isAbs(`/workspace`) || p.isAbs(`workspace`) {
		t.Fatal("POSIX absolute-path detection changed")
	}
	if got, err := p.resolve(`/workspace`, `nested/result.json`); err != nil || got != "/workspace/nested/result.json" {
		t.Fatalf("resolve relative = %q, %v", got, err)
	}
	if got, err := p.resolve(`/workspace`, `/tmp/result.json`); err != nil || got != "/tmp/result.json" {
		t.Fatalf("resolve absolute = %q, %v", got, err)
	}
	if _, err := p.resolve(`/workspace`, `../escape`); err == nil {
		t.Fatal("resolve traversal returned nil error")
	}
	if got, err := p.relative(`/workspace/src`, `/workspace/src/a/b.txt`); err != nil || got != "a/b.txt" {
		t.Fatalf("relative = %q, %v", got, err)
	}
}

func TestTargetPathWindows(t *testing.T) {
	t.Parallel()

	p := targetPathFor(platform.GOOSWindows)
	if got := p.clean(`C:/workspace/src/../result.json`); got != `C:\workspace\result.json` {
		t.Fatalf("clean = %q", got)
	}
	if got := p.dir(`C:\workspace\result.json`); got != `C:\workspace` {
		t.Fatalf("dir = %q", got)
	}
	if !p.isAbs(`C:\workspace`) || !p.isAbs(`c:/workspace`) || p.isAbs(`\workspace`) || p.isAbs(`workspace`) {
		t.Fatal("Windows absolute-path detection changed")
	}
	if got, err := p.resolve(`C:\workspace`, `nested/result.json`); err != nil || got != `C:\workspace\nested\result.json` {
		t.Fatalf("resolve relative = %q, %v", got, err)
	}
	if got, err := p.resolve(`C:\workspace`, `D:/temp/result.json`); err != nil || got != `D:\temp\result.json` {
		t.Fatalf("resolve absolute = %q, %v", got, err)
	}
	if _, err := p.resolve(`C:\workspace`, `..\escape`); err == nil {
		t.Fatal("resolve traversal returned nil error")
	}
	if got, err := p.relative(`C:\Workspace\src`, `c:/workspace/src/a/b.txt`); err != nil || got != `a\b.txt` {
		t.Fatalf("relative = %q, %v", got, err)
	}
	if got := p.toSlash(`a\b.txt`); got != "a/b.txt" {
		t.Fatalf("toSlash = %q", got)
	}
}

func TestTargetPathRelativeRejectsOutsideRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		p    targetPath
		root string
		file string
	}{
		{name: "posix sibling", p: targetPathFor(platform.GOOSLinux), root: "/workspace/src", file: "/workspace/other.txt"},
		{name: "windows sibling", p: targetPathFor(platform.GOOSWindows), root: `C:\workspace\src`, file: `C:\workspace\other.txt`},
		{name: "windows other drive", p: targetPathFor(platform.GOOSWindows), root: `C:\workspace`, file: `D:\workspace\file.txt`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := tt.p.relative(tt.root, tt.file); err == nil {
				t.Fatal("relative outside root returned nil error")
			}
		})
	}
}
