package judge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

const (
	judgeContextProfileMinimal  = "minimal"
	judgeContextProfileStandard = "standard"

	judgeContextModeInclude  = "include"
	judgeContextModeOmit     = "omit"
	judgeContextModeTruncate = "truncate"
	judgeContextModeFileRef  = "file_ref"

	judgeContextGeneratedOmit    = "omit"
	judgeContextGeneratedIndex   = "index"
	judgeContextGeneratedInclude = "include"

	defaultJudgeContextMaxBytes = 64 * 1024
	judgeContextArtifactDir     = "judge/context"
)

// MaterializedContext describes the files and inline previews made available
// to an agent_judge invocation.
type MaterializedContext struct {
	Dir           string
	RuntimeDir    string
	Configuration string
	SkillUsage    *SkillUsageEvidence
	Manifest      ContextManifest
	Materials     []ContextMaterial
}

// ContextManifest is persisted as manifest.json and copied into reports.
type ContextManifest struct {
	Profile         string                    `json:"profile"`
	MaterializedDir string                    `json:"materialized_dir,omitempty"`
	RuntimeDir      string                    `json:"runtime_dir,omitempty"`
	Materials       []ContextMaterialManifest `json:"materials"`
}

// ContextMaterialManifest records one material's delivery decision.
type ContextMaterialManifest struct {
	Key           string `json:"key"`
	Label         string `json:"label,omitempty"`
	Mode          string `json:"mode"`
	Path          string `json:"path,omitempty"`
	Bytes         int    `json:"bytes"`
	OriginalBytes int    `json:"original_bytes,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
}

// ContextMaterial is a prompt-facing material entry.
type ContextMaterial struct {
	ContextMaterialManifest

	InlineContent string `json:"-"`
	RuntimePath   string `json:"-"`
}

type effectiveJudgeContext struct {
	profile               string
	finalMessageMode      string
	transcriptMode        string
	workspaceDiffMode     string
	generatedFilesMode    string
	skillSourceMode       string
	maxBytes              int
	transcriptMaxTurns    int
	workspaceDiffMaxLines int
	attachments           []config.JudgeContextAttachment
}

// MaterializeJudgeContext writes agent_judge materials to disk and uploads
// readable copies into the runtime workspace.
func MaterializeJudgeContext(ctx context.Context, rt runtime.Runtime, cfg *config.JudgeContextConfig, in Input, artifactDir string) (*MaterializedContext, error) {
	effective := resolveJudgeContext(cfg)
	hostDir, err := judgeContextHostDir(artifactDir)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(hostDir); err != nil {
		return nil, fmt.Errorf("clear judge context dir: %w", err)
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return nil, fmt.Errorf("create judge context dir: %w", err)
	}

	runtimeDir := ".skill-up/judge/context"
	if rt != nil && strings.TrimSpace(rt.Workspace()) != "" {
		runtimeDir = filepath.Join(rt.Workspace(), runtimeDir)
	}
	mc := &MaterializedContext{
		Dir:           hostDir,
		RuntimeDir:    runtimeDir,
		Configuration: in.Configuration,
		SkillUsage:    in.SkillUsage,
		Manifest: ContextManifest{
			Profile:         effective.profile,
			MaterializedDir: judgeContextArtifactDir,
		},
	}

	if err := materializeText(ctx, rt, mc, "final_message", "final_message.txt", in.FinalMessage, effective.finalMessageMode, effective.maxBytes); err != nil {
		return nil, err
	}
	transcriptJSON, err := marshalTranscript(in.Transcript, effective.transcriptMaxTurns)
	if err != nil {
		return nil, err
	}
	if err := materializeText(ctx, rt, mc, "transcript", "transcript.json", transcriptJSON, effective.transcriptMode, effective.maxBytes); err != nil {
		return nil, err
	}
	diff := limitLines(in.WorkspaceDiff, effective.workspaceDiffMaxLines)
	if err := materializeText(ctx, rt, mc, "workspace_diff", "workspace.diff", diff, effective.workspaceDiffMode, effective.maxBytes); err != nil {
		return nil, err
	}
	if err := materializeExtendedContext(ctx, rt, mc, in, effective); err != nil {
		return nil, err
	}
	if err := materializeAttachments(ctx, rt, mc, effective.attachments, in); err != nil {
		return nil, err
	}

	manifestPath := filepath.Join(hostDir, "manifest.json")
	data, err := json.MarshalIndent(mc.Manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal judge context manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		return nil, fmt.Errorf("write judge context manifest: %w", err)
	}
	if err := uploadRuntimeFile(ctx, rt, manifestPath, filepath.Join(runtimeDir, "manifest.json")); err != nil {
		return nil, err
	}

	logging.InfoContextf(ctx, "judge context materialized profile=%s dir=%s", effective.profile, hostDir)
	return mc, nil
}

func resolveJudgeContext(cfg *config.JudgeContextConfig) effectiveJudgeContext {
	profile := judgeContextProfileStandard
	if cfg != nil && cfg.Profile != "" {
		profile = cfg.Profile
	}
	effective := effectiveJudgeContext{
		profile:               profile,
		maxBytes:              defaultJudgeContextMaxBytes,
		generatedFilesMode:    judgeContextGeneratedIndex,
		skillSourceMode:       judgeContextModeFileRef,
		workspaceDiffMaxLines: 0,
	}
	switch profile {
	case judgeContextProfileMinimal:
		effective.finalMessageMode = judgeContextModeTruncate
		effective.transcriptMode = judgeContextModeOmit
		effective.workspaceDiffMode = judgeContextModeOmit
		effective.generatedFilesMode = judgeContextGeneratedOmit
		effective.skillSourceMode = judgeContextModeOmit
	default:
		effective.finalMessageMode = judgeContextModeInclude
		effective.transcriptMode = judgeContextModeFileRef
		effective.workspaceDiffMode = judgeContextModeFileRef
	}
	if cfg == nil {
		return effective
	}
	if cfg.FinalMessage != "" {
		effective.finalMessageMode = cfg.FinalMessage
	}
	if cfg.Transcript != "" {
		effective.transcriptMode = cfg.Transcript
	}
	if cfg.WorkspaceDiff != "" {
		effective.workspaceDiffMode = cfg.WorkspaceDiff
	}
	if cfg.GeneratedFiles != "" {
		effective.generatedFilesMode = cfg.GeneratedFiles
	}
	if cfg.SkillSource != "" {
		effective.skillSourceMode = cfg.SkillSource
	}
	if cfg.Limits != nil {
		if cfg.Limits.MaxBytes > 0 {
			effective.maxBytes = cfg.Limits.MaxBytes
		}
		effective.transcriptMaxTurns = cfg.Limits.TranscriptMaxTurns
		effective.workspaceDiffMaxLines = cfg.Limits.WorkspaceDiffMaxLines
	}
	effective.attachments = append([]config.JudgeContextAttachment(nil), cfg.Attachments...)
	return effective
}

func judgeContextHostDir(artifactDir string) (string, error) {
	if artifactDir == "" {
		return os.MkdirTemp("", "skill-up-judge-context-*")
	}
	return filepath.Join(filepath.Dir(artifactDir), "context"), nil
}

func materializeText(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, key, fileName, content, mode string, maxBytes int) error {
	if mode == "" {
		mode = judgeContextModeFileRef
	}
	material := ContextMaterial{
		ContextMaterialManifest: ContextMaterialManifest{
			Key:           key,
			Mode:          mode,
			Bytes:         len([]byte(content)),
			OriginalBytes: len([]byte(content)),
		},
	}
	if strings.TrimSpace(content) == "" && mode != judgeContextModeOmit {
		mode = judgeContextModeOmit
		material.Mode = mode
	}
	if mode == judgeContextModeOmit {
		material.Bytes = 0
		material.OriginalBytes = 0
		mc.Manifest.Materials = append(mc.Manifest.Materials, material.ContextMaterialManifest)
		return nil
	}

	if mode == judgeContextModeInclude && len([]byte(content)) > maxBytes {
		mode = judgeContextModeFileRef
		material.Mode = mode
	}
	runtimePath, manifestPath, err := writeMaterialFile(ctx, rt, mc, fileName, content)
	if err != nil {
		return err
	}
	material.Path = manifestPath
	material.RuntimePath = runtimePath
	switch mode {
	case judgeContextModeInclude:
		material.InlineContent = content
	case judgeContextModeTruncate:
		material.InlineContent = truncateBytes(content, maxBytes)
		material.Bytes = len([]byte(material.InlineContent))
		material.Truncated = material.Bytes < material.OriginalBytes
	case judgeContextModeFileRef:
	default:
		return fmt.Errorf("unsupported judge context mode %q for %s", mode, key)
	}
	mc.Manifest.Materials = append(mc.Manifest.Materials, material.ContextMaterialManifest)
	mc.Materials = append(mc.Materials, material)
	return nil
}

func writeMaterialFile(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, fileName, content string) (runtimePath string, manifestPath string, err error) {
	safeName, err := safeMaterialRelPath(fileName)
	if err != nil {
		return "", "", err
	}
	hostPath, err := safeMaterialHostPath(mc.Dir, safeName)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return "", "", fmt.Errorf("create material dir: %w", err)
	}
	// #nosec G703 -- hostPath is constrained by safeMaterialRelPath and safeMaterialHostPath to stay under mc.Dir.
	if err := os.WriteFile(hostPath, []byte(content), 0o600); err != nil {
		return "", "", fmt.Errorf("write material %s: %w", fileName, err)
	}
	runtimePath = materialRuntimePath(mc, safeName)
	if err := uploadRuntimeFile(ctx, rt, hostPath, runtimePath); err != nil {
		return "", "", err
	}
	return runtimePath, materialManifestPath(safeName), nil
}

func copyMaterialFile(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, fileName, sourcePath string) (runtimePath string, manifestPath string, bytes int, err error) {
	safeName, err := safeMaterialRelPath(fileName)
	if err != nil {
		return "", "", 0, err
	}
	hostPath, err := safeMaterialHostPath(mc.Dir, safeName)
	if err != nil {
		return "", "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return "", "", 0, fmt.Errorf("create material dir: %w", err)
	}
	// #nosec G304 -- sourcePath is resolved by resolveAttachmentPath to stay within skill/workspace roots.
	src, err := os.Open(sourcePath)
	if err != nil {
		return "", "", 0, fmt.Errorf("read judge context attachment %q: %w", sourcePath, err)
	}
	// #nosec G304 -- hostPath is constrained by safeMaterialRelPath and safeMaterialHostPath to stay under mc.Dir.
	dst, err := os.OpenFile(hostPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		if closeErr := src.Close(); closeErr != nil {
			return "", "", 0, fmt.Errorf("close judge context attachment %q: %w", sourcePath, closeErr)
		}
		return "", "", 0, fmt.Errorf("write material %s: %w", fileName, err)
	}
	copied, copyErr := io.Copy(dst, src)
	dstCloseErr := dst.Close()
	srcCloseErr := src.Close()
	if copyErr != nil {
		return "", "", 0, fmt.Errorf("copy material %s: %w", fileName, copyErr)
	}
	if dstCloseErr != nil {
		return "", "", 0, fmt.Errorf("close material %s: %w", fileName, dstCloseErr)
	}
	if srcCloseErr != nil {
		return "", "", 0, fmt.Errorf("close judge context attachment %q: %w", sourcePath, srcCloseErr)
	}
	if copied > int64(^uint(0)>>1) {
		return "", "", 0, fmt.Errorf("material %s is too large", fileName)
	}
	runtimePath = materialRuntimePath(mc, safeName)
	if err := uploadRuntimeFile(ctx, rt, hostPath, runtimePath); err != nil {
		return "", "", 0, err
	}
	return runtimePath, materialManifestPath(safeName), int(copied), nil
}

func materialRuntimePath(mc *MaterializedContext, safeName string) string {
	return filepath.Join(mc.RuntimeDir, filepath.ToSlash(safeName))
}

func materialManifestPath(safeName string) string {
	return pathpkg.Join(judgeContextArtifactDir, filepath.ToSlash(safeName))
}

func safeMaterialRelPath(fileName string) (string, error) {
	if fileName == "" || filepath.IsAbs(fileName) {
		return "", fmt.Errorf("invalid judge context material path %q", fileName)
	}
	clean := filepath.Clean(fileName)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("invalid judge context material path %q", fileName)
	}
	return clean, nil
}

func safeMaterialHostPath(root, rel string) (string, error) {
	hostPath := filepath.Join(root, rel)
	cleanRoot := filepath.Clean(root)
	cleanHostPath := filepath.Clean(hostPath)
	if !isWithin(cleanHostPath, cleanRoot) {
		return "", fmt.Errorf("judge context material path escapes context dir: %q", rel)
	}
	return cleanHostPath, nil
}

func uploadRuntimeFile(ctx context.Context, rt runtime.Runtime, hostPath, runtimePath string) error {
	if rt == nil {
		return nil
	}
	if err := rt.UploadFile(ctx, hostPath, runtimePath); err != nil {
		return fmt.Errorf("upload judge context file %s: %w", filepath.Base(hostPath), err)
	}
	return nil
}

func materializeGeneratedFiles(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, files []string, mode string, maxBytes int) error {
	if mode == "" {
		mode = judgeContextGeneratedIndex
	}
	if mode == judgeContextGeneratedOmit || len(files) == 0 {
		mc.Manifest.Materials = append(mc.Manifest.Materials, ContextMaterialManifest{
			Key:  "generated_files",
			Mode: judgeContextGeneratedOmit,
		})
		return nil
	}
	content := strings.Join(files, "\n")
	if content != "" {
		content += "\n"
	}
	textMode := judgeContextModeFileRef
	if mode == judgeContextGeneratedInclude && len([]byte(content)) <= maxBytes {
		textMode = judgeContextModeInclude
	}
	return materializeText(ctx, rt, mc, "generated_files", "generated_files.txt", content, textMode, maxBytes)
}

type skillSourceIndex struct {
	Configuration string                  `json:"configuration"`
	Skills        []skillSourceIndexEntry `json:"skills"`
}

type skillSourceIndexEntry struct {
	Name  string                 `json:"name"`
	Files []skillSourceIndexFile `json:"files"`
}

type skillSourceIndexFile struct {
	Path         string `json:"path"`
	Bytes        int    `json:"bytes"`
	SHA256       string `json:"sha256"`
	MaterialPath string `json:"material_path,omitempty"`
}

// CaptureSkillSources freezes the evaluated Skill inputs before Agent
// execution. The returned snapshots are bounded by the effective judge context
// limit and can be materialized later without re-reading mutable source paths.
func CaptureSkillSources(sources []SkillSource, cfg *config.JudgeContextConfig) ([]SkillSource, error) {
	effective := resolveJudgeContext(cfg)
	if effective.skillSourceMode == judgeContextModeOmit || len(sources) == 0 {
		return nil, nil
	}
	return captureSkillSourcesWithLimit(sources, effective.maxBytes)
}

func materializeSkillSources(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, in Input, mode string, maxBytes int) error {
	if mode == judgeContextModeOmit || in.Configuration == configurationWithoutSkill {
		return materializeText(ctx, rt, mc, "skill_source", "skill_source/index.json", "", judgeContextModeOmit, maxBytes)
	}

	sources := append([]SkillSource(nil), in.SkillSources...)
	if len(sources) == 0 {
		return materializeText(ctx, rt, mc, "skill_source", "skill_source/index.json", "", judgeContextModeOmit, maxBytes)
	}
	if !skillSourcesCaptured(sources) {
		var err error
		sources, err = captureSkillSourcesWithLimit(sources, maxBytes)
		if err != nil {
			return err
		}
	}

	index := skillSourceIndex{Configuration: normalizedConfiguration(in.Configuration)}
	for sourceIndex, source := range sources {
		entry, err := materializeCapturedSkillSource(ctx, rt, mc, sourceIndex, source)
		if err != nil {
			return err
		}
		index.Skills = append(index.Skills, entry)
	}

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal skill source index: %w", err)
	}
	data = append(data, '\n')
	return materializeText(ctx, rt, mc, "skill_source", "skill_source/index.json", string(data), judgeContextModeFileRef, maxBytes)
}

func materializeDiagnosticContext(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, in Input, effective effectiveJudgeContext) error {
	if err := materializeSkillSources(ctx, rt, mc, in, effective.skillSourceMode, effective.maxBytes); err != nil {
		// Skill source is diagnostic enrichment, not verdict input required by
		// the existing contract. Preserve evaluation compatibility by recording
		// it as unavailable when snapshotting fails.
		logging.WarnContextf(ctx, "Judge context: evaluated Skill source unavailable: %v", err)
		if fallbackErr := materializeText(ctx, rt, mc, "skill_source", "skill_source/index.json", "", judgeContextModeOmit, effective.maxBytes); fallbackErr != nil {
			return fallbackErr
		}
	}
	return materializeSkillUsage(ctx, rt, mc, in.SkillUsage, effective.maxBytes)
}

func materializeExtendedContext(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, in Input, effective effectiveJudgeContext) error {
	if err := materializeGeneratedFiles(ctx, rt, mc, in.GeneratedFiles, effective.generatedFilesMode, effective.maxBytes); err != nil {
		return err
	}
	return materializeDiagnosticContext(ctx, rt, mc, in, effective)
}

func captureSkillSourcesWithLimit(sources []SkillSource, maxBytes int) ([]SkillSource, error) {
	captured := make([]SkillSource, 0, len(sources))
	remainingBytes := maxBytes
	for _, source := range sources {
		snapshot, nextRemaining, err := captureOneSkillSource(source, remainingBytes)
		if err != nil {
			return nil, err
		}
		captured = append(captured, snapshot)
		remainingBytes = nextRemaining
	}
	return captured, nil
}

func captureOneSkillSource(source SkillSource, remainingBytes int) (SkillSource, int, error) {
	if source.Captured {
		return cloneCapturedSkillSource(source, remainingBytes)
	}

	files, err := agent.ListSkillFiles(source.Path, source.Include, source.Exclude)
	if err != nil {
		return SkillSource{}, remainingBytes, fmt.Errorf("list evaluated skill source %q: %w", source.Path, err)
	}
	sortSkillSourceFiles(files)

	snapshot := SkillSource{
		Name:     skillSourceName(source),
		Path:     source.Path,
		Include:  append([]string(nil), source.Include...),
		Exclude:  append([]string(nil), source.Exclude...),
		Captured: true,
		Files:    []SkillSourceFile{},
	}
	for _, rel := range files {
		sourcePath := filepath.Join(source.Path, rel)
		info, statErr := os.Lstat(sourcePath)
		if statErr != nil {
			return SkillSource{}, remainingBytes, fmt.Errorf("stat evaluated skill source %q: %w", sourcePath, statErr)
		}
		// Do not follow symlinks while creating judge-only review material: a
		// Skill source may contain a link outside its root, and the diagnostic
		// snapshot must not turn that into an unintended file disclosure.
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		var content *boundedCaptureWriter
		if isReadableSkillSourceFile(rel) {
			content = &boundedCaptureWriter{limit: remainingBytes}
		}
		file, captureErr := captureSkillSourceFile(sourcePath, rel, content)
		if captureErr != nil {
			return SkillSource{}, remainingBytes, captureErr
		}
		if file.HasContent {
			remainingBytes -= file.Bytes
		}
		snapshot.Files = append(snapshot.Files, file)
	}
	return snapshot, remainingBytes, nil
}

func materializeCapturedSkillSource(
	ctx context.Context,
	rt runtime.Runtime,
	mc *MaterializedContext,
	sourceIndex int,
	source SkillSource,
) (skillSourceIndexEntry, error) {
	name := skillSourceName(source)
	entry := skillSourceIndexEntry{Name: name, Files: []skillSourceIndexFile{}}
	for _, snapshot := range source.Files {
		file := skillSourceIndexFile{Path: snapshot.Path, Bytes: snapshot.Bytes, SHA256: snapshot.SHA256}
		if snapshot.HasContent {
			materialSubdir := fmt.Sprintf("%02d-%s", sourceIndex+1, name)
			materialName := filepath.Join("skill_source", materialSubdir, filepath.FromSlash(snapshot.Path))
			if _, _, writeErr := writeMaterialFile(ctx, rt, mc, materialName, string(snapshot.Content)); writeErr != nil {
				return skillSourceIndexEntry{}, writeErr
			}
			file.MaterialPath = filepath.ToSlash(filepath.Join(materialSubdir, filepath.FromSlash(snapshot.Path)))
		}
		entry.Files = append(entry.Files, file)
	}
	return entry, nil
}

func materializeSkillUsage(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, usage *SkillUsageEvidence, maxBytes int) error {
	if usage == nil {
		usage = &SkillUsageEvidence{Status: SkillUsageUnavailable}
	}
	mc.SkillUsage = usage
	data, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal skill usage evidence: %w", err)
	}
	data = append(data, '\n')
	return materializeText(ctx, rt, mc, "skill_usage", "skill_usage.json", string(data), judgeContextModeInclude, maxBytes)
}

func normalizedConfiguration(configuration string) string {
	if configuration == configurationWithoutSkill {
		return configuration
	}
	return configurationWithSkill
}

func captureSkillSourceFile(path, rel string, content *boundedCaptureWriter) (SkillSourceFile, error) {
	// #nosec G304 -- path is selected from an evaluated Skill source directory by ListSkillFiles.
	file, err := os.Open(path)
	if err != nil {
		return SkillSourceFile{}, fmt.Errorf("read evaluated skill source %q: %w", path, err)
	}
	hash := sha256.New()
	writer := io.Writer(hash)
	if content != nil {
		writer = io.MultiWriter(hash, content)
	}
	bytes, copyErr := io.Copy(writer, file)
	closeErr := file.Close()
	if copyErr != nil {
		return SkillSourceFile{}, fmt.Errorf("snapshot evaluated skill source %q: %w", path, copyErr)
	}
	if closeErr != nil {
		return SkillSourceFile{}, fmt.Errorf("close evaluated skill source %q: %w", path, closeErr)
	}
	if bytes > int64(^uint(0)>>1) {
		return SkillSourceFile{}, fmt.Errorf("evaluated skill source %q is too large", path)
	}
	snapshot := SkillSourceFile{
		Path:   filepath.ToSlash(rel),
		Bytes:  int(bytes),
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}
	if content != nil && !content.overflow {
		snapshot.Content = content.data
		snapshot.HasContent = true
	}
	return snapshot, nil
}

type boundedCaptureWriter struct {
	limit    int
	data     []byte
	overflow bool
}

func (w *boundedCaptureWriter) Write(p []byte) (int, error) {
	remaining := w.limit - len(w.data)
	if remaining > 0 {
		copyBytes := min(remaining, len(p))
		w.data = append(w.data, p[:copyBytes]...)
	}
	if len(p) > remaining {
		w.overflow = true
	}
	return len(p), nil
}

func skillSourcesCaptured(sources []SkillSource) bool {
	for _, source := range sources {
		if !source.Captured {
			return false
		}
	}
	return true
}

func sortSkillSourceFiles(files []string) {
	sort.SliceStable(files, func(i, j int) bool {
		iSkill := strings.EqualFold(filepath.Base(files[i]), "SKILL.md")
		jSkill := strings.EqualFold(filepath.Base(files[j]), "SKILL.md")
		if iSkill != jSkill {
			return iSkill
		}
		return filepath.ToSlash(files[i]) < filepath.ToSlash(files[j])
	})
}

func skillSourceName(source SkillSource) string {
	name := source.Name
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(filepath.Clean(source.Path))
	}
	return safeMaterialName(name)
}

func cloneCapturedSkillSource(source SkillSource, remainingBytes int) (SkillSource, int, error) {
	clone := source
	clone.Include = append([]string(nil), source.Include...)
	clone.Exclude = append([]string(nil), source.Exclude...)
	clone.Files = make([]SkillSourceFile, len(source.Files))
	for i, file := range source.Files {
		clone.Files[i] = file
		clone.Files[i].Content = append([]byte(nil), file.Content...)
		if file.HasContent {
			if file.Bytes <= remainingBytes {
				remainingBytes -= file.Bytes
				continue
			}
			clone.Files[i].Content = nil
			clone.Files[i].HasContent = false
		}
	}
	return clone, remainingBytes, nil
}

func isReadableSkillSourceFile(path string) bool {
	if strings.EqualFold(filepath.Base(path), "SKILL.md") {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt", ".yaml", ".yml", ".json", ".toml", ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".sh", ".ps1":
		return true
	default:
		return false
	}
}

func materializeAttachments(ctx context.Context, rt runtime.Runtime, mc *MaterializedContext, attachments []config.JudgeContextAttachment, in Input) error {
	if len(attachments) == 0 {
		return nil
	}
	for i, attachment := range attachments {
		src, err := resolveAttachmentPath(attachment.Path, in)
		if err != nil {
			return err
		}
		label := attachment.Label
		if label == "" {
			label = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
		}
		name := fmt.Sprintf("%02d-%s%s", i+1, safeMaterialName(label), filepath.Ext(src))
		runtimePath, manifestPath, bytes, err := copyMaterialFile(ctx, rt, mc, filepath.Join("attachments", name), src)
		if err != nil {
			return err
		}
		material := ContextMaterial{
			ContextMaterialManifest: ContextMaterialManifest{
				Key:           "attachment",
				Label:         label,
				Mode:          judgeContextModeFileRef,
				Path:          manifestPath,
				Bytes:         bytes,
				OriginalBytes: bytes,
			},
			RuntimePath: runtimePath,
		}
		mc.Manifest.Materials = append(mc.Manifest.Materials, material.ContextMaterialManifest)
		mc.Materials = append(mc.Materials, material)
	}
	return nil
}

func resolveAttachmentPath(raw string, in Input) (string, error) {
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(in.SkillDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve judge context attachment %q: %w", raw, err)
	}
	for _, root := range []string{in.SkillDir, in.WorkspacePath} {
		if root == "" {
			continue
		}
		if isWithin(abs, root) {
			return abs, nil
		}
	}
	if in.SkillDir == "" && in.WorkspacePath == "" {
		return abs, nil
	}
	return "", fmt.Errorf("judge context attachment %q must stay within skill_dir or workspace", raw)
}

func isWithin(path, root string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func marshalTranscript(trans transcript.Transcript, maxTurns int) (string, error) {
	if len(trans) == 0 {
		return "", nil
	}
	if maxTurns > 0 && len(trans) > maxTurns {
		trans = trans[len(trans)-maxTurns:]
	}
	data, err := json.MarshalIndent(trans, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal transcript: %w", err)
	}
	return string(data), nil
}

func limitLines(s string, maxLines int) string {
	if maxLines <= 0 || s == "" {
		return s
	}
	lines := strings.SplitAfter(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "")
}

func truncateBytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(s)) <= maxBytes {
		return s
	}
	b := []byte(s)
	return string(b[:maxBytes])
}

func safeMaterialName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "attachment"
	}
	return name
}
