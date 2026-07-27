package runtime

import (
	"fmt"
	"path"
	"strings"

	"github.com/alibaba/skill-up/internal/platform"
)

// targetPath applies path semantics for a runtime guest independently of the
// skill-up host operating system.
type targetPath struct {
	windows bool
}

func targetPathFor(goos string) targetPath {
	return targetPath{windows: goos == platform.GOOSWindows}
}

func (p targetPath) clean(value string) string {
	if !p.windows {
		return path.Clean(value)
	}

	value = strings.ReplaceAll(value, "/", `\`)
	volume := ""
	rest := value
	if len(rest) >= 2 && rest[1] == ':' {
		volume, rest = rest[:2], rest[2:]
	}
	rooted := strings.HasPrefix(rest, `\`)
	clean := path.Clean(strings.ReplaceAll(rest, `\`, "/"))
	clean = strings.ReplaceAll(clean, "/", `\`)
	if rooted {
		clean = strings.TrimPrefix(clean, `\`)
		if clean == "" || clean == "." {
			return volume + `\`
		}
		return volume + `\` + clean
	}
	return volume + clean
}

func (p targetPath) dir(value string) string {
	clean := p.clean(value)
	if !p.windows {
		return path.Dir(clean)
	}
	idx := strings.LastIndex(clean, `\`)
	if idx < 0 {
		return "."
	}
	if idx == 2 && len(clean) >= 2 && clean[1] == ':' {
		return clean[:3]
	}
	if idx == 0 {
		return `\`
	}
	return clean[:idx]
}

func (p targetPath) isAbs(value string) bool {
	if !p.windows {
		return strings.HasPrefix(value, "/")
	}
	return len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func (p targetPath) join(base, elem string) string {
	if p.isAbs(elem) {
		return p.clean(elem)
	}
	sep := "/"
	if p.windows {
		sep = `\`
	}
	return p.clean(strings.TrimSuffix(base, sep) + sep + elem)
}

func (p targetPath) resolve(workspace, value string) (string, error) {
	if value == "" || value == "." {
		return p.clean(workspace), nil
	}
	if p.isAbs(value) {
		return p.clean(value), nil
	}
	clean := p.clean(value)
	sep := "/"
	if p.windows {
		sep = `\`
		if strings.Contains(clean, ":") {
			return "", fmt.Errorf("unsafe guest path %q", value)
		}
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+sep) {
		return "", fmt.Errorf("guest path %q escapes workspace %s", value, workspace)
	}
	return p.join(workspace, clean), nil
}

func (p targetPath) relative(root, file string) (string, error) {
	root = p.clean(root)
	file = p.clean(file)
	if p.equal(root, file) {
		return ".", nil
	}
	sep := "/"
	if p.windows {
		sep = `\`
	}
	prefix := root
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	if !p.hasPrefix(file, prefix) {
		return "", fmt.Errorf("sandbox search result %s is outside source directory %s", file, root)
	}
	return file[len(prefix):], nil
}

func (p targetPath) toSlash(value string) string {
	if p.windows {
		return strings.ReplaceAll(value, `\`, "/")
	}
	return value
}

func (p targetPath) equal(left, right string) bool {
	if p.windows {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (p targetPath) hasPrefix(value, prefix string) bool {
	if p.windows {
		return strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix))
	}
	return strings.HasPrefix(value, prefix)
}
