package redlines

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Render produces matrix.md. The output depends only on the matrix and on
// which server cases are written (WrittenTests), so it can be compared byte
// for byte with the committed file.
func Render(m *Matrix, written map[string]bool) []byte {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# 红线对照表\n\n")
	w("> 由 `contract/redlines/redlines.json` 生成，请勿手改。改完 JSON 后在 `tools/conformance` 下运行 `go run ./cmd/redlines write`；`go run ./cmd/redlines check` 校验它与场景、与本文件是否一致。\n\n")
	w("%s\n\n", m.About)

	w("## 概况\n\n")
	var server, client, withGolden, steps, planned, serverCases, serverWritten int
	scenarios := map[string]bool{}
	byPlatform := map[string]int{}
	byLayer := map[string]int{}
	byRef := map[string]int{}
	for _, rl := range m.Redlines {
		if isClient(rl.ID) {
			client++
		} else {
			server++
			byRef[rl.Reference]++
		}
		if len(rl.Golden) > 0 {
			withGolden++
		}
		for _, g := range rl.Golden {
			scenarios[g.Scenario] = true
			steps += len(g.Steps)
		}
		for _, p := range rl.Planned {
			planned++
			byPlatform[p.Platform]++
			byLayer[p.Layer]++
			if p.Platform == "server" {
				serverCases++
				if written[p.Name] {
					serverWritten++
				}
			}
		}
	}
	w("- 红线 %d 条：服务端与宿主 %d 条（R），客户端 %d 条（C）。\n", server+client, server, client)
	w("- 已有黄金用例的红线 %d 条，共引用 %d 个场景的 %d 处步骤。\n", withGolden, len(scenarios), steps)
	w("- R 类红线在参照服务上的现状：%s。\n", joinCounts(byRef, m.References, ordered(m.References, referenceOrder)))
	w("- 计划用例 %d 个。按平台：%s。\n", planned, joinCounts(byPlatform, m.Platforms, ordered(m.Platforms, platformOrder)))
	w("- 按测试层：%s。\n", joinCounts(byLayer, m.Layers, ordered(m.Layers, layerOrder)))
	w("- 服务端用例 %d 个，已写 %d 个。\n\n", serverCases, serverWritten)

	w("图例：\n\n")
	w("- 参照服务：小桃子原版服务（源提交 33e23250）在黄金对话里的表现。“违反”的红线在 legacy 档按原样录、在 strict 档由计划用例修正；“未观测”是黄金对话录不到，只能靠计划用例。\n")
	w("- 已录用例：已经录成黄金对话的场景与步号，ImKit 服务端用一致性测试台逐帧比对，三端 SDK 用回放服务器重放。\n")
	w("- 计划用例：自动化用例，标明计划任务号与平台，用例名带红线编号；并发压力用例重复 100 轮并开竞态检测，单次通过不算数。服务端已经写好的标“已写”：`redlines check` 扫描 `server/` 下的测试函数得出，按红线命名（`TestR<n>_…`）却不在本表里的测试会被报出来。\n\n")

	w("## 总表\n\n")
	w("| 编号 | 红线 | 平台 | 参照服务 | 已录用例 | 计划用例 |\n")
	w("| --- | --- | --- | --- | --- | --- |\n")
	for _, rl := range m.Redlines {
		w("| [%s](#%s) | %s | %s | %s | %s | %s |\n",
			rl.ID, strings.ToLower(rl.ID), cell(rl.Title), platformNames(m, rl.Platforms),
			cell(m.References[rl.Reference]), goldenSummary(rl), plannedSummary(m, rl, written))
	}
	w("\n")

	w("## 逐条明细\n\n")
	for _, rl := range m.Redlines {
		w("<a id=\"%s\"></a>\n\n### %s %s\n\n", strings.ToLower(rl.ID), rl.ID, rl.Title)
		w("- 平台：%s\n", platformNames(m, rl.Platforms))
		w("- 参照服务：%s\n", m.References[rl.Reference])
		if rl.Note != "" {
			w("- 说明：%s\n", rl.Note)
		}
		if len(rl.Golden) > 0 {
			w("- 已录用例：\n")
			for _, g := range rl.Golden {
				w("  - `%s` 第 %s 步（%s）：%s\n", g.Scenario, joinInts(g.StepNumbers()), verbs(g), g.Checks)
			}
		}
		if rl.NoGolden != "" {
			w("- 没有黄金用例：%s\n", rl.NoGolden)
		}
		if len(rl.Planned) > 0 {
			w("- 计划用例：\n")
			for _, p := range rl.Planned {
				extra := ""
				if p.Scenario != "" {
					extra = fmt.Sprintf("，用 `%s`", p.Scenario)
				}
				if p.Platform == "server" && written[p.Name] {
					extra += "，已写"
				}
				w("  - `%s`（%s，%s %s，%s%s）：%s\n", p.Name, m.Platforms[p.Platform], p.Task, m.Tasks[p.Task], m.Layers[p.Layer], extra, p.Checks)
			}
		}
		w("\n")
	}
	return []byte(b.String())
}

// Display orders for the summary; keys the registry adds later follow in
// alphabetical order.
var (
	platformOrder  = []string{"server", "host", "android", "web", "ios"}
	layerOrder     = []string{"unit", "concurrency", "sql-scan", "contract", "strict", "replay", "replay-fault", "ui", "device"}
	referenceOrder = []string{"holds", "partial", "violates", "unobserved", "n/a"}
)

func ordered(m map[string]string, preferred []string) []string {
	out := make([]string, 0, len(m))
	seen := map[string]bool{}
	for _, k := range preferred {
		if _, ok := m[k]; ok {
			out = append(out, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func joinCounts(counts map[string]int, names map[string]string, order []string) string {
	var parts []string
	for _, k := range order {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", names[k], counts[k]))
		}
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, "、")
}

func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func platformNames(m *Matrix, ps []string) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = m.Platforms[p]
	}
	return strings.Join(names, "、")
}

func goldenSummary(rl Redline) string {
	if len(rl.Golden) == 0 {
		return "—"
	}
	parts := make([]string, len(rl.Golden))
	for i, g := range rl.Golden {
		parts[i] = fmt.Sprintf("`%s` %s", g.Scenario, joinInts(g.StepNumbers()))
	}
	return strings.Join(parts, "<br>")
}

// plannedSummary lists each test name once with the platforms and tasks that
// carry it, e.g. "C1_X：Android 3.10、Web 5.8、iOS 6.6"; a written server case
// reads "服务端 1.11 已写".
func plannedSummary(m *Matrix, rl Redline, written map[string]bool) string {
	if len(rl.Planned) == 0 {
		return "—"
	}
	var order []string
	where := map[string][]string{}
	for _, p := range rl.Planned {
		if _, ok := where[p.Name]; !ok {
			order = append(order, p.Name)
		}
		place := m.Platforms[p.Platform] + " " + p.Task
		if p.Platform == "server" && written[p.Name] {
			place += " 已写"
		}
		where[p.Name] = append(where[p.Name], place)
	}
	parts := make([]string, len(order))
	for i, name := range order {
		parts[i] = fmt.Sprintf("`%s`：%s", name, strings.Join(where[name], "、"))
	}
	return strings.Join(parts, "<br>")
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, "、")
}

func verbs(g Golden) string {
	parts := make([]string, 0, len(g.Steps))
	for _, ref := range g.Steps {
		if _, verb, ok := parseStepRef(ref); ok {
			parts = append(parts, verb)
		}
	}
	return strings.Join(parts, "、")
}
