package runtime

import "context"

// JudgeSnapshotProvider is an optional runtime capability for independent judges.
// CaptureJudgeSnapshot must freeze the completed workspace before any judge runs.
// Implementations must not expose a live workspace as a frozen snapshot.
type JudgeSnapshotProvider interface {
	CaptureJudgeSnapshot(ctx context.Context) (JudgeSnapshot, error)
}

// JudgeSnapshot creates isolated runtimes from the same frozen workspace.
// Fork returns a ready runtime and its cleanup function. Writes in one fork must
// not change the snapshot, another fork, or the original agent workspace.
// The caller cleans each fork before closing the snapshot. Isolation outside the
// workspace (host state, network, external services) is implementation-specific.
type JudgeSnapshot interface {
	Fork(ctx context.Context) (Runtime, func(), error)
	Close() error
}

var (
	_ JudgeSnapshotProvider = (*NoneRuntime)(nil)
	_ JudgeSnapshot         = (*noneJudgeSnapshot)(nil)
)
