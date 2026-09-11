/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package output provides table, JSON, and YAML formatters for the kubezap CLI.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// Format represents an output format supported by CLI commands.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

// ParseFormat parses a format string into a Format constant.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "table", "":
		return FormatTable, nil
	case "json":
		return FormatJSON, nil
	case "yaml":
		return FormatYAML, nil
	default:
		return FormatTable, fmt.Errorf("unknown output format %q: must be table, json, or yaml", s)
	}
}

// PrintTable writes a tab-aligned table to w. The first row is the header.
func PrintTable(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

// PrintJSON writes v to w as indented JSON.
func PrintJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// PrintYAML writes v to w as YAML using sigs.k8s.io/yaml.
func PrintYAML(w io.Writer, v any) error {
	b, err := sigsyaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// FmtAge returns a human-readable elapsed-time string for a metav1.Time,
// e.g. "3d", "2h", "5m", "just now". Returns "-" for a zero time.
func FmtAge(t metav1.Time) string {
	if t.IsZero() {
		return "-"
	}
	return FmtDuration(time.Since(t.Time))
}

// FmtDuration formats a duration as a compact human-readable string.
func FmtDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FmtStepDuration formats a duration between two optional metav1.Times.
// Returns "-" if either time is nil.
func FmtStepDuration(start, end *metav1.Time) string {
	if start == nil || end == nil {
		return "-"
	}
	d := end.Sub(start.Time)
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%.0fms", float64(d.Milliseconds()))
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// Dash returns "-" for an empty string.
func Dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
