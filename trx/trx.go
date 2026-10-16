// Package trx converts the TRX results dotnet test writes into JUnit XML,
// which CircleCI's test results take.
package trx

import (
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

type testRun struct {
	Times struct {
		Start string `xml:"start,attr"`
	} `xml:"Times"`
	Results struct {
		Results []unitTestResult `xml:"UnitTestResult"`
	} `xml:"Results"`
	Definitions struct {
		Tests []unitTest `xml:"UnitTest"`
	} `xml:"TestDefinitions"`
}

type unitTestResult struct {
	TestID   string `xml:"testId,attr"`
	TestName string `xml:"testName,attr"`
	Duration string `xml:"duration,attr"`
	Outcome  string `xml:"outcome,attr"`
	Output   struct {
		StdOut    string `xml:"StdOut"`
		StdErr    string `xml:"StdErr"`
		ErrorInfo struct {
			Message    string `xml:"Message"`
			StackTrace string `xml:"StackTrace"`
		} `xml:"ErrorInfo"`
	} `xml:"Output"`
	Inner struct {
		Results []unitTestResult `xml:"UnitTestResult"`
	} `xml:"InnerResults"`
}

type unitTest struct {
	ID     string `xml:"id,attr"`
	Method struct {
		ClassName string `xml:"className,attr"`
		CodeBase  string `xml:"codeBase,attr"`
	} `xml:"TestMethod"`
}

// Suite is one JUnit testsuite.
type Suite struct {
	XMLName   xml.Name `xml:"testsuite"`
	Name      string   `xml:"name,attr"`
	Tests     int      `xml:"tests,attr"`
	Failures  int      `xml:"failures,attr"`
	Skipped   int      `xml:"skipped,attr"`
	Time      string   `xml:"time,attr"`
	Timestamp string   `xml:"timestamp,attr,omitempty"`
	Cases     []Case   `xml:"testcase"`
}

// Case is one JUnit testcase.
type Case struct {
	ClassName string   `xml:"classname,attr"`
	Name      string   `xml:"name,attr"`
	Time      string   `xml:"time,attr"`
	File      string   `xml:"file,attr,omitempty"`
	Failure   *Message `xml:"failure"`
	Skipped   *Message `xml:"skipped"`
	SystemOut string   `xml:"system-out,omitempty"`
	SystemErr string   `xml:"system-err,omitempty"`
}

// Message is a failure or skip, with its detail as the element's text.
type Message struct {
	Message string `xml:"message,attr,omitempty"`
	Text    string `xml:",chardata"`
}

// Convert reads a TRX document into a JUnit suite named name, or when name
// is empty after the test assembly. file, when set, becomes every case's file
// attribute, which CircleCI splits tests by.
func Convert(r io.Reader, name, file string) (*Suite, error) {
	var run testRun
	if err := xml.NewDecoder(r).Decode(&run); err != nil {
		return nil, fmt.Errorf("parse TRX: %w", err)
	}
	classes := make(map[string]string, len(run.Definitions.Tests))
	for _, t := range run.Definitions.Tests {
		classes[t.ID] = t.Method.ClassName
		if name == "" && t.Method.CodeBase != "" {
			base := path.Base(strings.ReplaceAll(t.Method.CodeBase, `\`, "/"))
			name = strings.TrimSuffix(base, path.Ext(base))
		}
	}

	suite := &Suite{Name: name, Timestamp: timestamp(run.Times.Start)}
	var total float64
	for _, res := range flatten(run.Results.Results) {
		c := newCase(res, classes[res.TestID], file)
		switch {
		case c.Failure != nil:
			suite.Failures++
		case c.Skipped != nil:
			suite.Skipped++
		}
		total += seconds(res.Duration)
		suite.Cases = append(suite.Cases, c)
	}
	suite.Tests = len(suite.Cases)
	suite.Time = formatSeconds(total)
	return suite, nil
}

// flatten replaces a data-driven result with the results of its rows.
func flatten(results []unitTestResult) []unitTestResult {
	var out []unitTestResult
	for _, r := range results {
		if len(r.Inner.Results) == 0 {
			out = append(out, r)
			continue
		}
		for _, inner := range flatten(r.Inner.Results) {
			if inner.TestID == "" {
				inner.TestID = r.TestID
			}
			out = append(out, inner)
		}
	}
	return out
}

func newCase(r unitTestResult, class, file string) Case {
	c := Case{
		ClassName: class,
		Name:      strings.TrimPrefix(r.TestName, class+"."),
		Time:      formatSeconds(seconds(r.Duration)),
		File:      file,
		SystemOut: r.Output.StdOut,
		SystemErr: r.Output.StdErr,
	}
	info := r.Output.ErrorInfo
	switch outcome(r.Outcome) {
	case failed:
		text := strings.TrimSpace(info.Message)
		if st := strings.TrimSpace(info.StackTrace); st != "" {
			text += "\n" + st
		}
		c.Failure = &Message{Message: firstLine(info.Message), Text: text}
	case skipped:
		c.Skipped = &Message{Message: firstLine(info.Message)}
	}
	return c
}

type result int

const (
	passed result = iota
	failed
	skipped
)

func outcome(s string) result {
	switch s {
	case "Failed", "Error", "Timeout", "Aborted", "Disconnected":
		return failed
	case "NotExecuted", "NotRunnable", "Inconclusive", "Pending", "InProgress":
		return skipped
	}
	return passed
}

// seconds parses a TRX duration, [d.]hh:mm:ss[.fffffff].
func seconds(d string) float64 {
	days := 0.0
	if day, rest, ok := strings.Cut(d, "."); ok && !strings.Contains(day, ":") {
		n, _ := strconv.ParseFloat(day, 64)
		days, d = n, rest
	}
	parts := strings.Split(d, ":")
	if len(parts) != 3 {
		return 0
	}
	h, _ := strconv.ParseFloat(parts[0], 64)
	m, _ := strconv.ParseFloat(parts[1], 64)
	s, _ := strconv.ParseFloat(parts[2], 64)
	return days*86400 + h*3600 + m*60 + s
}

func formatSeconds(s float64) string {
	return strconv.FormatFloat(s, 'f', 3, 64)
}

func timestamp(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// Write writes suites as a JUnit document.
func Write(w io.Writer, suites ...*Suite) error {
	doc := struct {
		XMLName xml.Name `xml:"testsuites"`
		Suites  []*Suite `xml:"testsuite"`
	}{Suites: suites}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
