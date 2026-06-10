package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/alibaba/skill-up/internal/config"
)

// maxHTTPUploadBytes caps the total size of files uploaded in a single
// multipart request, so a broad glob (e.g. **/*) over a large workspace cannot
// exhaust memory while the body is assembled in-process.
const maxHTTPUploadBytes = 256 * 1024 * 1024

// buildMultipartBody assembles a multipart/form-data body: a `payload` field
// carrying the rendered JSON request body, plus one `files` part per matched
// workspace file (the part filename is the workspace-relative path). It returns
// the body bytes and the multipart Content-Type (which carries the boundary).
func (t *httpTransport) buildMultipartBody(ctx context.Context, rt Runtime, h *config.CustomHTTPConfig, payload []byte) ([]byte, string, error) {
	relPaths, err := expandHTTPFiles(ctx, rt, h.Files)
	if err != nil {
		return nil, "", err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	field, err := mw.CreateFormField("payload")
	if err != nil {
		return nil, "", fmt.Errorf("http.files: write payload field: %w", err)
	}
	if _, err := field.Write(payload); err != nil {
		return nil, "", fmt.Errorf("http.files: write payload field: %w", err)
	}

	var total int64
	for _, rel := range relPaths {
		if err := addFilePart(ctx, rt, mw, rel, &total); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("http.files: close multipart: %w", err)
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// addFilePart downloads rel to a per-call temp file, enforces the cumulative
// upload cap using the file's size (so an oversized file is rejected before it
// is loaded into memory), then streams it into a `files` multipart part. The
// part filename is the workspace-relative path. The whole multipart body is
// still buffered in memory, bounded by maxHTTPUploadBytes; with many parallel
// cases a streaming (io.Pipe) body would lower peak RAM — a future improvement.
func addFilePart(ctx context.Context, rt Runtime, mw *multipart.Writer, rel string, total *int64) error {
	tmpFile, err := os.CreateTemp("", "skill-up-http-upload-*")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmp) }()

	if err := rt.DownloadFile(ctx, rel, tmp); err != nil {
		return fmt.Errorf("http.files: read %q: %w", rel, err)
	}
	info, err := os.Stat(tmp)
	if err != nil {
		return fmt.Errorf("http.files: stat %q: %w", rel, err)
	}
	*total += info.Size()
	if *total > maxHTTPUploadBytes {
		return fmt.Errorf("http.files: total upload exceeds %d bytes", maxHTTPUploadBytes)
	}

	f, err := os.Open(tmp)
	if err != nil {
		return fmt.Errorf("http.files: open %q: %w", rel, err)
	}
	defer func() { _ = f.Close() }()
	part, err := mw.CreateFormFile("files", rel)
	if err != nil {
		return fmt.Errorf("http.files: create part for %q: %w", rel, err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("http.files: write %q: %w", rel, err)
	}
	return nil
}

// expandHTTPFiles resolves the declared file set against the runtime workspace.
// Each entry's path is an exact workspace-relative path or a doublestar glob;
// only regular files are uploaded (the workspace listing excludes directories
// and .git). With required (the default), an entry that matches nothing is an
// error; with required:false it is skipped. Results are deduplicated and sorted
// for a deterministic request.
func expandHTTPFiles(ctx context.Context, rt Runtime, files []config.CustomHTTPFile) ([]string, error) {
	listing, err := listWorkspaceFiles(ctx, rt)
	if err != nil {
		return nil, fmt.Errorf("http.files: list workspace: %w", err)
	}
	present := make(map[string]bool, len(listing))
	for _, rel := range listing {
		present[rel] = true
	}

	seen := make(map[string]bool)
	var out []string
	for i, f := range files {
		if !config.WorkspaceRelPathSafe(f.Path) {
			return nil, fmt.Errorf(
				"engine.custom.http.files[%d].path %q must be a non-empty relative path inside the workspace", i, f.Path)
		}
		var matched []string
		if isGlobPattern(f.Path) {
			for _, rel := range listing {
				ok, mErr := doublestar.Match(f.Path, rel)
				if mErr != nil {
					return nil, fmt.Errorf("engine.custom.http.files[%d].path %q: invalid glob: %w", i, f.Path, mErr)
				}
				if ok {
					matched = append(matched, rel)
				}
			}
		} else if present[f.Path] {
			matched = []string{f.Path}
		}
		if len(matched) == 0 {
			if f.Required == nil || *f.Required {
				return nil, fmt.Errorf("engine.custom.http.files[%d]: %q matched no files in the workspace", i, f.Path)
			}
			continue
		}
		for _, m := range matched {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// isGlobPattern reports whether p contains glob metacharacters and so must be
// expanded rather than treated as an exact path.
func isGlobPattern(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

// listWorkspaceFiles returns the workspace-relative paths of every regular file
// in the runtime workspace (excluding .git). It mirrors the evaluator's
// artifacts_collect.listWorkspaceFiles; the two are kept separate only because
// agent cannot import evaluator (evaluator already imports agent). A shared
// leaf package would consolidate them (see issue #50's theme).
func listWorkspaceFiles(ctx context.Context, rt Runtime) ([]string, error) {
	// -print0 (NUL-separated) so a filename containing a newline cannot split
	// one entry into two — otherwise a crafted name like "a\n/etc/passwd" would
	// yield a bogus absolute entry that a glob could match and read off the
	// workspace.
	result, err := rt.Exec(ctx, "find . -type f -not -path './.git/*' -print0", ExecOptions{Cwd: rt.Workspace()})
	if err != nil {
		return nil, err
	}
	var files []string
	for entry := range strings.SplitSeq(result.Stdout, "\x00") {
		if entry == "" {
			continue
		}
		rel := strings.TrimPrefix(entry, "./")
		// Defence in depth: never surface an absolute or ..-bearing entry so it
		// can never reach DownloadFile, regardless of how find behaves. Also drop
		// names with a CR/LF — they cannot be safely written into a multipart
		// Content-Disposition filename (header corruption / injection).
		if rel != "" && config.WorkspaceRelPathSafe(rel) && !strings.ContainsAny(rel, "\r\n") {
			files = append(files, rel)
		}
	}
	return files, nil
}
