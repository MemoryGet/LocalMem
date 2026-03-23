// Package testreport 测试报告记录与生成 / Test report recording and generation
package testreport

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// Report 测试报告 / Test report
type Report struct {
	mu        sync.Mutex
	Project   string   `json:"project"`
	Generated string   `json:"generated"`
	Duration  string   `json:"duration"`
	Suites    []*Suite `json:"suites"`
	Summary   Summary  `json:"summary"`
	suiteMap  map[string]*Suite
	startTime time.Time
}

// Summary 报告摘要 / Report summary
type Summary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Suite 测试套件 / Test suite
type Suite struct {
	Name  string  `json:"name"`
	Icon  string  `json:"icon"`
	Desc  string  `json:"desc"`
	Cases []*Case `json:"cases"`
}

// Case 测试用例 / Test case
type Case struct {
	Name     string  `json:"name"`
	Desc     string  `json:"desc,omitempty"`
	Status   string  `json:"status"`
	Duration string  `json:"duration"`
	Inputs   []Field `json:"inputs"`
	Steps    []Step  `json:"steps"`
	Outputs  []Field `json:"outputs"`
	Error    string  `json:"error,omitempty"`

	t     *testing.T
	start time.Time
	step  int
}

// Field 键值对 / Key-value field
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Kind  string `json:"kind,omitempty"` // text / code / json / sql
}

// Step 测试步骤 / Test step
type Step struct {
	Order  int    `json:"order"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status"` // ok / fail / info
}

var globalReport *Report

// Init 初始化报告 / Initialize report
func Init(project string) {
	globalReport = &Report{
		Project:   project,
		Generated: time.Now().Format("2006-01-02 15:04:05"),
		suiteMap:  make(map[string]*Suite),
		startTime: time.Now(),
	}
}

// NewCase 创建测试用例 / Create test case
func NewCase(t *testing.T, suiteName, suiteIcon, suiteDesc, caseName string) *Case {
	if globalReport == nil {
		Init("IClude")
	}

	c := &Case{
		Name:  caseName,
		t:     t,
		start: time.Now(),
	}

	globalReport.mu.Lock()
	suite, ok := globalReport.suiteMap[suiteName]
	if !ok {
		suite = &Suite{Name: suiteName, Icon: suiteIcon, Desc: suiteDesc}
		globalReport.suiteMap[suiteName] = suite
		globalReport.Suites = append(globalReport.Suites, suite)
	}
	suite.Cases = append(suite.Cases, c)
	globalReport.mu.Unlock()

	return c
}

// Description 记录用例说明 / Record case description
func (c *Case) Description(desc string) *Case {
	c.Desc = desc
	emitJSON(map[string]any{"type": "description", "name": c.Name, "desc": desc})
	return c
}

// Input 记录输入 / Record input
func (c *Case) Input(label, value string) *Case {
	c.Inputs = append(c.Inputs, Field{Label: label, Value: value})
	emitJSON(map[string]any{"type": "input", "name": c.Name, "label": label, "value": value})
	return c
}

// InputCode 记录代码输入 / Record code input
func (c *Case) InputCode(label, value string) *Case {
	c.Inputs = append(c.Inputs, Field{Label: label, Value: value, Kind: "code"})
	emitJSON(map[string]any{"type": "input", "name": c.Name, "label": label, "value": value})
	return c
}

// InputSQL 记录 SQL 输入 / Record SQL input
func (c *Case) InputSQL(label, value string) *Case {
	c.Inputs = append(c.Inputs, Field{Label: label, Value: value, Kind: "sql"})
	emitJSON(map[string]any{"type": "input", "name": c.Name, "label": label, "value": value})
	return c
}

// Step 记录成功步骤 / Record successful step
func (c *Case) Step(action string, detail ...string) *Case {
	c.step++
	d := ""
	if len(detail) > 0 {
		d = detail[0]
	}
	c.Steps = append(c.Steps, Step{Order: c.step, Action: action, Detail: d, Status: "ok"})
	emitJSON(map[string]any{"type": "step", "name": c.Name, "seq": c.step, "action": action, "detail": d, "status": "ok"})
	return c
}

// StepInfo 记录信息步骤 / Record info step
func (c *Case) StepInfo(action string, detail ...string) *Case {
	c.step++
	d := ""
	if len(detail) > 0 {
		d = detail[0]
	}
	c.Steps = append(c.Steps, Step{Order: c.step, Action: action, Detail: d, Status: "info"})
	emitJSON(map[string]any{"type": "step", "name": c.Name, "seq": c.step, "action": action, "detail": d, "status": "info"})
	return c
}

// StepFail 记录失败步骤 / Record failed step
func (c *Case) StepFail(action string, detail ...string) *Case {
	c.step++
	d := ""
	if len(detail) > 0 {
		d = detail[0]
	}
	c.Steps = append(c.Steps, Step{Order: c.step, Action: action, Detail: d, Status: "fail"})
	emitJSON(map[string]any{"type": "step", "name": c.Name, "seq": c.step, "action": action, "detail": d, "status": "fail"})
	return c
}

// Output 记录输出 / Record output
func (c *Case) Output(label, value string) *Case {
	c.Outputs = append(c.Outputs, Field{Label: label, Value: value})
	emitJSON(map[string]any{"type": "output", "name": c.Name, "label": label, "value": value})
	return c
}

// OutputCode 记录代码输出 / Record code output
func (c *Case) OutputCode(label, value string) *Case {
	c.Outputs = append(c.Outputs, Field{Label: label, Value: value, Kind: "code"})
	emitJSON(map[string]any{"type": "output", "name": c.Name, "label": label, "value": value})
	return c
}

// Done 完成用例记录 / Finalize case
func (c *Case) Done() {
	c.Duration = fmt.Sprintf("%.1fms", float64(time.Since(c.start).Microseconds())/1000.0)
	if c.t.Failed() {
		c.Status = "fail"
	} else if c.t.Skipped() {
		c.Status = "skip"
	} else {
		c.Status = "pass"
	}
	emitJSON(map[string]any{"type": "case_end", "name": c.Name, "status": c.Status, "duration": c.Duration})
}

// emitJSON 向 stdout 发送 ##TESTREPORT## JSON 行 / Emit JSON line to stdout for dashboard
func emitJSON(data map[string]any) {
	if os.Getenv("TESTREPORT_JSON") != "1" {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stdout, "##TESTREPORT##%s\n", b)
}
