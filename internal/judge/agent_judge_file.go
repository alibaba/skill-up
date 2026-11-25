package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/logging"
)

const agentJudgeFileMode = "file"

type judgeOutputFile struct {
	runtimePath string
	hostPath    string
}

func (j *AgentJudge) prepareOutputFile(ctx context.Context, artifactDir string, attempt int) (judgeOutputFile, error) {
	if j.OutputMode != agentJudgeFileMode {
		return judgeOutputFile{}, nil
	}
	if j.Runtime == nil || strings.TrimSpace(j.Runtime.Workspace()) == "" {
		return judgeOutputFile{}, errors.New("agent_judge file output requires a runtime workspace")
	}
	// A fresh directory also prevents a retry from accepting the first attempt's file.
	dir, err := os.MkdirTemp("", "skill-up-judge-result-*")
	if err != nil {
		return judgeOutputFile{}, err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			logging.WarnContextf(ctx, "remove temporary judge result directory: %v", err)
		}
	}()
	template := judgeResponse{Results: make([]CriterionResult, len(j.Criteria))}
	for i := range j.Criteria {
		template.Results[i] = CriterionResult{CriterionID: criterionID(i), Evidence: []string{}, Failures: []string{}}
	}
	data, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return judgeOutputFile{}, err
	}
	source := filepath.Join(dir, "judge-result.json")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		return judgeOutputFile{}, err
	}
	runtimePath := filepath.Join(j.Runtime.Workspace(), ".skill-up", "judge", filepath.Base(dir), "judge-result.json")
	if err := j.Runtime.UploadFile(ctx, source, runtimePath); err != nil {
		return judgeOutputFile{}, fmt.Errorf("prepare judge result template: %w", err)
	}
	hostPath := ""
	if artifactDir != "" {
		hostPath = filepath.Join(artifactDir, fmt.Sprintf("result-attempt-%d.json", attempt))
	}
	return judgeOutputFile{runtimePath: runtimePath, hostPath: hostPath}, nil
}

func (j *AgentJudge) readJudgeResults(ctx context.Context, session *agent.SessionResult, file judgeOutputFile, runErr error) ([]CriterionResult, error) {
	if session == nil {
		return nil, errors.New("agent returned no session result")
	}
	if file.runtimePath == "" {
		return parseAgentJudgeResults(j.Criteria, session.FinalMessage)
	}
	// A completed file may remain recoverable after a deadline, just like chat
	// output. Bound only this transfer; never resume agent work or cancellation.
	if runErr != nil && !errors.Is(runErr, context.Canceled) && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	dir, err := os.MkdirTemp("", "skill-up-judge-download-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			logging.WarnContextf(ctx, "remove temporary judge result directory: %v", err)
		}
	}()
	target := filepath.Join(dir, "result.json")
	if err := j.Runtime.DownloadFile(ctx, file.runtimePath, target); err != nil {
		return nil, fmt.Errorf("read judge result file: %w", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("read downloaded judge result: %w", err)
	}
	if file.hostPath != "" {
		if err := os.MkdirAll(filepath.Dir(file.hostPath), 0o700); err != nil {
			return nil, err
		}
		root, err := os.OpenRoot(filepath.Dir(file.hostPath))
		if err != nil {
			return nil, err
		}
		writeErr := root.WriteFile(filepath.Base(file.hostPath), data, 0o600)
		closeErr := root.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return nil, err
		}
	}
	// File delivery promises pure JSON, while chat delivery also permits one fence.
	if !json.Valid(data) {
		return nil, errors.New("judge result file must contain pure JSON")
	}
	return parseAgentJudgeResults(j.Criteria, string(data))
}
