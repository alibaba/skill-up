package evaluator

import (
	"fmt"

	"github.com/alibaba/skill-up/internal/config"
)

const (
	// ConfigurationWithSkill executes a case with the evaluated skill installed.
	ConfigurationWithSkill = "with_skill"
	// ConfigurationWithoutSkill executes a case without the evaluated skill.
	ConfigurationWithoutSkill = "without_skill"
)

// Plan is the immutable task expansion for one evaluation invocation.
type Plan struct {
	StartIteration int
	Iterations     []IterationPlan
	TaskTotal      int
}

// IterationPlan contains the tasks assigned to one iteration.
type IterationPlan struct {
	Number int
	Tasks  []PlannedTask
}

// PlannedTask identifies one case and configuration execution within a Plan.
type PlannedTask struct {
	ID            string
	GlobalIndex   int
	GlobalTotal   int
	Iteration     int
	Case          *config.CaseConfig
	Configuration string
}

// BuildPlan deterministically expands cases, iterations, and configurations.
func BuildPlan(cases []*config.CaseConfig, startIteration, iterationCount int, configurations []string) Plan {
	if startIteration < 1 {
		startIteration = 1
	}
	if iterationCount < 1 {
		iterationCount = 1
	}

	configurationCount := len(configurations)
	taskTotal := len(cases) * iterationCount * configurationCount
	plan := Plan{
		StartIteration: startIteration,
		Iterations:     make([]IterationPlan, 0, iterationCount),
		TaskTotal:      taskTotal,
	}

	globalIndex := 0
	for iterationOffset := range iterationCount {
		iteration := IterationPlan{
			Number: startIteration + iterationOffset,
			Tasks:  make([]PlannedTask, 0, len(cases)*configurationCount),
		}
		for _, caseCfg := range cases {
			for _, configuration := range configurations {
				globalIndex++
				iteration.Tasks = append(iteration.Tasks, newPlannedTask(
					globalIndex,
					taskTotal,
					iteration.Number,
					caseCfg,
					configuration,
				))
			}
		}
		plan.Iterations = append(plan.Iterations, iteration)
	}

	return plan
}

func newPlannedTask(index, total, iteration int, caseCfg *config.CaseConfig, configuration string) PlannedTask {
	return PlannedTask{
		ID:            fmt.Sprintf("task-%d", index),
		GlobalIndex:   index,
		GlobalTotal:   total,
		Iteration:     iteration,
		Case:          caseCfg,
		Configuration: configuration,
	}
}
