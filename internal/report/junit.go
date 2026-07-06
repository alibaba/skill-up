package report

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/judge"
)

// JUnitReporter writes JUnit XML for CI systems.
//
// Mapping: each case → <testcase>, failed assertions → <failure> message.
type JUnitReporter struct {
	// OutputPath is the file path to write the JUnit XML report.
	// If empty, writes to stdout.
	OutputPath string
}

// Write implements the Reporter interface.
func (r *JUnitReporter) Write(_ context.Context, in Input) error {
	suite := r.buildTestSuite(in)

	return writeToOutput(r.OutputPath, "junit report", func(w io.Writer) error {
		if _, err := w.Write([]byte(xml.Header)); err != nil {
			return fmt.Errorf("write junit xml header: %w", err)
		}

		enc := xml.NewEncoder(w)
		enc.Indent("", "  ")
		if err := enc.Encode(suite); err != nil {
			return fmt.Errorf("encode junit xml: %w", err)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// JUnit XML model
// ---------------------------------------------------------------------------

// junitTestSuites is the top-level <testsuites> element.
type junitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestSuite `xml:"testsuite"`
}

// junitTestSuite is a <testsuite> element.
type junitTestSuite struct {
	XMLName   xml.Name        `xml:"testsuite"`
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr"`
	Timestamp string          `xml:"timestamp,attr"`
	TestCases []junitTestCase `xml:"testcase"`
}

// junitTestCase is a <testcase> element.
type junitTestCase struct {
	XMLName   xml.Name      `xml:"testcase"`
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitError   `xml:"error,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

// junitFailure is a <failure> element.
type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

// junitError is an <error> element.
type junitError struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

// junitSkipped is a <skipped> element.
type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// ---------------------------------------------------------------------------
// Builder
// ---------------------------------------------------------------------------

func (r *JUnitReporter) buildTestSuite(in Input) junitTestSuites {
	var (
		failures int
		errors   int
		skipped  int
		cases    []junitTestCase
	)

	for _, cr := range in.CaseResults {
		tc := junitTestCase{
			Name:      cr.CaseID,
			ClassName: in.SkillName,
			Time:      fmt.Sprintf("%.3f", float64(cr.DurationMs)/1000.0),
		}

		switch cr.Status {
		case judge.StatusFail:
			failures++
			tc.Failure = &junitFailure{
				Message: fmt.Sprintf("case %s failed", cr.CaseID),
				Type:    "AssertionFailure",
				Body:    buildFailureBody(cr),
			}
		case judge.StatusError:
			errors++
			errMsg := cr.Error
			if errMsg == "" && cr.Grading != nil && cr.Grading.ErrorReason != nil {
				errMsg = *cr.Grading.ErrorReason
			}
			tc.Error = &junitError{
				Message: errMsg,
				Type:    "ExecutionError",
				Body:    errMsg,
			}
		case judge.StatusSkip:
			skipped++
			msg := ""
			if cr.Grading != nil && cr.Grading.SkipReason != nil {
				msg = *cr.Grading.SkipReason
			}
			tc.Skipped = &junitSkipped{Message: msg}
		}

		cases = append(cases, tc)
	}

	totalTime := in.TotalDuration()

	suite := junitTestSuite{
		Name:      in.SkillName,
		Tests:     len(in.CaseResults),
		Failures:  failures,
		Errors:    errors,
		Skipped:   skipped,
		Time:      fmt.Sprintf("%.3f", totalTime.Seconds()),
		Timestamp: in.StartTime.Format(time.RFC3339),
		TestCases: cases,
	}

	return junitTestSuites{Suites: []junitTestSuite{suite}}
}

// buildFailureBody extracts failed assertion details for the <failure> body.
// Turn-scoped assertions already include turn numbers in their text field,
// making CI failure output directly actionable.
func buildFailureBody(cr CaseResult) string {
	if cr.Grading == nil {
		return ""
	}
	var lines []string
	for _, ar := range cr.Grading.AssertionResults {
		if !ar.Passed {
			lines = append(lines, fmt.Sprintf("- %s: %s", ar.Text, ar.Evidence))
		}
	}
	// Append turn summary when multi-turn results are present.
	if len(cr.TurnResults) > 0 {
		for _, tr := range cr.TurnResults {
			if tr.Status != "completed" {
				lines = append(lines, fmt.Sprintf("- turn %d: status=%s reason=%s", tr.TurnNumber, tr.Status, tr.Reason))
			}
		}
	}
	return strings.Join(lines, "\n")
}
