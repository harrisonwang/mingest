// mingest - Media Ingestion CLI tool
// Copyright (C) 2026  Harrison Wang <https://mingest.com>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package ingest

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type doctorOptions struct {
	AssetRef string
	Target   string
	Strict   bool
	JSON     bool
}

type doctorCheck struct {
	ID      string                 `json:"id"`
	Level   string                 `json:"level"` // pass|warn|fail
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

type doctorSummary struct {
	Total int `json:"total"`
	Pass  int `json:"pass"`
	Warn  int `json:"warn"`
	Fail  int `json:"fail"`
}

type doctorJSONResult struct {
	OK       bool          `json:"ok"`
	ExitCode int           `json:"exit_code"`
	Error    string        `json:"error,omitempty"`
	AssetID  string        `json:"asset_id,omitempty"`
	AssetRef string        `json:"asset_ref,omitempty"`
	Target   string        `json:"target,omitempty"`
	Strict   bool          `json:"strict,omitempty"`
	PrepPlan string        `json:"prep_plan,omitempty"`
	Summary  doctorSummary `json:"summary,omitempty"`
	Checks   []doctorCheck `json:"checks,omitempty"`
}

type doctorThreshold struct {
	ClipMinSec            float64
	ClipMaxSec            float64
	MaxOverlapRatio       float64
	MinSubtitleCoverage   float64
	MaxNearDuplicateScore float64
	MaxBoundaryCutRate    float64
}

func parseDoctorOptions(args []string) (doctorOptions, error) {
	opts := doctorOptions{
		Target: "youtube",
	}

	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--json":
			opts.JSON = true
		case arg == "--strict":
			opts.Strict = true
		case arg == "--target":
			if i+1 >= len(args) {
				return doctorOptions{}, fmt.Errorf("`--target` 缺少参数")
			}
			i++
			opts.Target = strings.ToLower(strings.TrimSpace(args[i]))
		case strings.HasPrefix(arg, "--target="):
			opts.Target = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--target=")))
		case strings.HasPrefix(arg, "-"):
			return doctorOptions{}, fmt.Errorf("不支持的参数: %s", arg)
		default:
			if opts.AssetRef != "" {
				return doctorOptions{}, fmt.Errorf("`mingest doctor` 仅支持一个 asset_ref")
			}
			opts.AssetRef = arg
		}
	}

	if strings.TrimSpace(opts.AssetRef) == "" {
		return doctorOptions{}, fmt.Errorf("缺少 asset_ref。用法: mingest doctor <asset_ref> [--target <youtube|bilibili|shorts>] [--strict] [--json]")
	}

	switch opts.Target {
	case "youtube", "bilibili", "shorts":
	default:
		return doctorOptions{}, fmt.Errorf("`--target` 仅支持 youtube|bilibili|shorts")
	}

	return opts, nil
}

func runDoctor(opts doctorOptions) int {
	asset, err := resolvePrepAsset(opts.AssetRef)
	if err != nil {
		return doctorExitWithErr(opts.JSON, exitDownloadFailed, err.Error())
	}
	if strings.TrimSpace(asset.AssetID) == "" {
		assetID, err := computeAssetID(asset.OutputPath)
		if err != nil {
			return doctorExitWithErr(opts.JSON, exitDownloadFailed, fmt.Sprintf("生成 asset_id 失败: %v", err))
		}
		asset.AssetID = assetID
	}

	_, prepPlanPath, err := latestPrepBundle(asset)
	if err != nil {
		return doctorExitWithErr(opts.JSON, exitDownloadFailed, err.Error())
	}

	plan, err := readPrepPlan(prepPlanPath)
	if err != nil {
		return doctorExitWithErr(opts.JSON, exitDownloadFailed, fmt.Sprintf("读取 prep-plan.json 失败: %v", err))
	}

	checks := runDoctorChecks(opts, plan)
	summary := summarizeDoctorChecks(checks)
	ok := summary.Fail == 0
	exitCode := exitOK
	if !ok {
		exitCode = exitDoctorFailed
	}

	if opts.JSON {
		result := doctorJSONResult{
			OK:       ok,
			ExitCode: exitCode,
			AssetID:  strings.TrimSpace(asset.AssetID),
			AssetRef: strings.TrimSpace(opts.AssetRef),
			Target:   opts.Target,
			Strict:   opts.Strict,
			PrepPlan: prepPlanPath,
			Summary:  summary,
			Checks:   checks,
		}
		printDoctorJSON(result)
		return exitCode
	}

	status := "PASS"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("asset_id: %s\n", strings.TrimSpace(asset.AssetID))
	fmt.Printf("asset_path: %s\n", asset.OutputPath)
	fmt.Printf("target: %s\n", opts.Target)
	fmt.Printf("strict: %v\n", opts.Strict)
	fmt.Printf("prep_plan: %s\n", prepPlanPath)
	fmt.Printf("doctor: %s (pass=%d warn=%d fail=%d)\n", status, summary.Pass, summary.Warn, summary.Fail)
	for _, c := range checks {
		fmt.Printf("[%s] %s: %s\n", strings.ToUpper(c.Level), c.ID, c.Message)
	}

	return exitCode
}

func runDoctorChecks(opts doctorOptions, plan prepPlan) []doctorCheck {
	threshold := doctorThresholdFor(opts.Target, opts.Strict)
	checks := make([]doctorCheck, 0, 12)

	clips := plan.Clips
	durationSec := plan.Probe.DurationSec
	if durationSec <= 0 {
		durationSec = sumClipDuration(clips)
	}

	checks = append(checks, doctorCheckClipCount(opts, clips))
	checks = append(checks, doctorCheckClipTimeline(clips, durationSec)...)
	checks = append(checks, doctorCheckClipDuration(opts, clips, threshold))
	checks = append(checks, doctorCheckOverlap(clips, threshold))

	cues, subtitlePath, hasRealSubtitle := loadDoctorSubtitle(plan)
	checks = append(checks, doctorCheckSubtitleSource(hasRealSubtitle, subtitlePath))

	if len(cues) == 0 {
		checks = append(checks, doctorCheck{
			ID:      "subtitle_coverage",
			Level:   "warn",
			Message: "未读取到可用字幕条目，无法评估字幕覆盖率与边界完整性",
		})
	} else {
		checks = append(checks, doctorCheckSubtitleCoverage(clips, cues, threshold))
		checks = append(checks, doctorCheckBoundaryCuts(clips, cues, threshold))
		checks = append(checks, doctorCheckNearDuplicate(clips, cues, threshold))
	}

	checks = append(checks, doctorCheckUniformPattern(clips))
	return checks
}

func doctorThresholdFor(target string, strict bool) doctorThreshold {
	t := doctorThreshold{
		ClipMinSec:            12,
		ClipMaxSec:            120,
		MaxOverlapRatio:       0.25,
		MinSubtitleCoverage:   0.50,
		MaxNearDuplicateScore: 0.85,
		MaxBoundaryCutRate:    0.55,
	}
	if target == "shorts" {
		t.ClipMinSec = 10
		t.ClipMaxSec = 65
		t.MaxOverlapRatio = 0.18
		t.MinSubtitleCoverage = 0.55
		t.MaxNearDuplicateScore = 0.80
		t.MaxBoundaryCutRate = 0.45
	}
	if strict {
		if target == "shorts" {
			t.ClipMinSec = 15
			t.ClipMaxSec = 45
		} else {
			t.ClipMinSec = 15
			t.ClipMaxSec = 90
		}
		t.MaxOverlapRatio = math.Max(0.10, t.MaxOverlapRatio-0.08)
		t.MinSubtitleCoverage = math.Min(0.80, t.MinSubtitleCoverage+0.10)
		t.MaxNearDuplicateScore = math.Max(0.72, t.MaxNearDuplicateScore-0.06)
		t.MaxBoundaryCutRate = math.Max(0.30, t.MaxBoundaryCutRate-0.10)
	}
	return t
}

func doctorCheckClipCount(opts doctorOptions, clips []prepClip) doctorCheck {
	if len(clips) == 0 {
		return doctorCheck{
			ID:      "clip_count",
			Level:   "fail",
			Message: "没有可用 clips。请先运行 `mingest prep` 或写入候选片段",
		}
	}
	if opts.Target == "shorts" && len(clips) != 3 {
		return doctorCheck{
			ID:      "clip_count",
			Level:   "warn",
			Message: fmt.Sprintf("shorts 目标建议 3 段，当前为 %d 段", len(clips)),
			Details: map[string]interface{}{"clip_count": len(clips)},
		}
	}
	return doctorCheck{
		ID:      "clip_count",
		Level:   "pass",
		Message: fmt.Sprintf("片段数量有效（%d 段）", len(clips)),
		Details: map[string]interface{}{"clip_count": len(clips)},
	}
}

func doctorCheckClipTimeline(clips []prepClip, durationSec float64) []doctorCheck {
	if len(clips) == 0 {
		return nil
	}
	invalid := 0
	oob := 0
	for _, c := range clips {
		if c.EndSec <= c.StartSec {
			invalid++
		}
		if durationSec > 0 && (c.StartSec < 0 || c.EndSec > durationSec+0.001) {
			oob++
		}
	}
	if invalid > 0 {
		return []doctorCheck{{
			ID:      "timeline_range",
			Level:   "fail",
			Message: fmt.Sprintf("发现 %d 段时间范围无效（end <= start）", invalid),
			Details: map[string]interface{}{"invalid_ranges": invalid},
		}}
	}
	if oob > 0 {
		return []doctorCheck{{
			ID:      "timeline_range",
			Level:   "fail",
			Message: fmt.Sprintf("发现 %d 段越界（超出视频总时长）", oob),
			Details: map[string]interface{}{"out_of_bounds": oob, "duration_sec": roundMillis(durationSec)},
		}}
	}
	return []doctorCheck{{
		ID:      "timeline_range",
		Level:   "pass",
		Message: "时间范围检查通过（无负时长、无越界）",
		Details: map[string]interface{}{"duration_sec": roundMillis(durationSec)},
	}}
}

func doctorCheckClipDuration(opts doctorOptions, clips []prepClip, threshold doctorThreshold) doctorCheck {
	if len(clips) == 0 {
		return doctorCheck{
			ID:      "clip_duration",
			Level:   "fail",
			Message: "无片段可检查",
		}
	}
	out := 0
	minSeen := math.MaxFloat64
	maxSeen := 0.0
	for _, c := range clips {
		d := c.DurationSec
		if d <= 0 && c.EndSec > c.StartSec {
			d = c.EndSec - c.StartSec
		}
		if d < minSeen {
			minSeen = d
		}
		if d > maxSeen {
			maxSeen = d
		}
		if d < threshold.ClipMinSec || d > threshold.ClipMaxSec {
			out++
		}
	}

	level := "pass"
	msg := fmt.Sprintf("片段时长处于建议范围 %.0f-%.0fs", threshold.ClipMinSec, threshold.ClipMaxSec)
	if out > 0 {
		level = "warn"
		if opts.Strict {
			level = "fail"
		}
		msg = fmt.Sprintf("有 %d 段不在建议时长范围 %.0f-%.0fs", out, threshold.ClipMinSec, threshold.ClipMaxSec)
	}
	return doctorCheck{
		ID:      "clip_duration",
		Level:   level,
		Message: msg,
		Details: map[string]interface{}{
			"min_sec": roundMillis(minSeen),
			"max_sec": roundMillis(maxSeen),
			"outlier": out,
		},
	}
}

func doctorCheckOverlap(clips []prepClip, threshold doctorThreshold) doctorCheck {
	if len(clips) < 2 {
		return doctorCheck{
			ID:      "clip_overlap",
			Level:   "pass",
			Message: "片段少于 2 段，无重叠风险",
		}
	}

	maxRatio := 0.0
	maxPair := ""
	for i := 0; i < len(clips); i++ {
		for j := i + 1; j < len(clips); j++ {
			ratio := doctorOverlapRatio(clips[i], clips[j])
			if ratio > maxRatio {
				maxRatio = ratio
				maxPair = fmt.Sprintf("%s/%s", doctorClipLabel(clips[i], i), doctorClipLabel(clips[j], j))
			}
		}
	}

	if maxRatio > threshold.MaxOverlapRatio {
		return doctorCheck{
			ID:      "clip_overlap",
			Level:   "warn",
			Message: fmt.Sprintf("片段重叠偏高（max_ratio=%.2f, pair=%s）", roundMillis(maxRatio), maxPair),
			Details: map[string]interface{}{
				"max_overlap_ratio": roundMillis(maxRatio),
				"max_allowed_ratio": threshold.MaxOverlapRatio,
				"pair":              maxPair,
			},
		}
	}
	return doctorCheck{
		ID:      "clip_overlap",
		Level:   "pass",
		Message: fmt.Sprintf("片段重叠可接受（max_ratio=%.2f）", roundMillis(maxRatio)),
		Details: map[string]interface{}{
			"max_overlap_ratio": roundMillis(maxRatio),
			"max_allowed_ratio": threshold.MaxOverlapRatio,
		},
	}
}

func doctorCheckSubtitleSource(hasRealSubtitle bool, subtitlePath string) doctorCheck {
	if !hasRealSubtitle {
		msg := "当前使用模板字幕或无字幕，语义评估可信度较低"
		if strings.TrimSpace(subtitlePath) != "" {
			msg += "（" + filepath.Base(subtitlePath) + "）"
		}
		return doctorCheck{
			ID:      "subtitle_source",
			Level:   "warn",
			Message: msg,
		}
	}
	return doctorCheck{
		ID:      "subtitle_source",
		Level:   "pass",
		Message: "检测到真实字幕输入，可用于语义与边界评估",
		Details: map[string]interface{}{"subtitle": subtitlePath},
	}
}

func doctorCheckSubtitleCoverage(clips []prepClip, cues []subtitleCue, threshold doctorThreshold) doctorCheck {
	if len(clips) == 0 {
		return doctorCheck{
			ID:      "subtitle_coverage",
			Level:   "fail",
			Message: "无片段可检查",
		}
	}
	coverages := make([]float64, 0, len(clips))
	below := 0
	for _, c := range clips {
		coverage := doctorClipSubtitleCoverage(c, cues)
		coverages = append(coverages, coverage)
		if coverage < threshold.MinSubtitleCoverage {
			below++
		}
	}
	avg := doctorMean(coverages)
	minCov := doctorMin(coverages)
	level := "pass"
	msg := fmt.Sprintf("字幕覆盖率达标（avg=%.2f,min=%.2f）", roundMillis(avg), roundMillis(minCov))
	if below > 0 {
		level = "warn"
		msg = fmt.Sprintf("有 %d 段字幕覆盖率低于阈值 %.2f（avg=%.2f,min=%.2f）", below, threshold.MinSubtitleCoverage, roundMillis(avg), roundMillis(minCov))
	}
	return doctorCheck{
		ID:      "subtitle_coverage",
		Level:   level,
		Message: msg,
		Details: map[string]interface{}{
			"avg_coverage":    roundMillis(avg),
			"min_coverage":    roundMillis(minCov),
			"below_threshold": below,
			"threshold":       threshold.MinSubtitleCoverage,
		},
	}
}

func doctorCheckBoundaryCuts(clips []prepClip, cues []subtitleCue, threshold doctorThreshold) doctorCheck {
	if len(clips) == 0 || len(cues) == 0 {
		return doctorCheck{
			ID:      "boundary_cut",
			Level:   "warn",
			Message: "无法评估边界切断率",
		}
	}
	cutCount := 0
	totalBoundary := len(clips) * 2
	for _, c := range clips {
		if doctorCutBoundary(c.StartSec, cues, true) {
			cutCount++
		}
		if doctorCutBoundary(c.EndSec, cues, false) {
			cutCount++
		}
	}
	rate := float64(cutCount) / float64(totalBoundary)
	if rate > threshold.MaxBoundaryCutRate {
		return doctorCheck{
			ID:      "boundary_cut",
			Level:   "warn",
			Message: fmt.Sprintf("边界切断率偏高（%.2f > %.2f）", roundMillis(rate), threshold.MaxBoundaryCutRate),
			Details: map[string]interface{}{
				"cut_rate":   roundMillis(rate),
				"cut_count":  cutCount,
				"boundaries": totalBoundary,
				"threshold":  threshold.MaxBoundaryCutRate,
			},
		}
	}
	return doctorCheck{
		ID:      "boundary_cut",
		Level:   "pass",
		Message: fmt.Sprintf("边界完整性可接受（cut_rate=%.2f）", roundMillis(rate)),
		Details: map[string]interface{}{
			"cut_rate":   roundMillis(rate),
			"cut_count":  cutCount,
			"boundaries": totalBoundary,
			"threshold":  threshold.MaxBoundaryCutRate,
		},
	}
}

func doctorCheckNearDuplicate(clips []prepClip, cues []subtitleCue, threshold doctorThreshold) doctorCheck {
	if len(clips) < 2 || len(cues) == 0 {
		return doctorCheck{
			ID:      "semantic_duplicate",
			Level:   "pass",
			Message: "片段不足以检查重复度",
		}
	}
	texts := make([]string, 0, len(clips))
	for _, c := range clips {
		texts = append(texts, doctorClipText(c, cues))
	}

	maxSim := 0.0
	maxPair := ""
	for i := 0; i < len(texts); i++ {
		for j := i + 1; j < len(texts); j++ {
			sim := doctorJaccardSimilarity(texts[i], texts[j])
			if sim > maxSim {
				maxSim = sim
				maxPair = fmt.Sprintf("%d/%d", i+1, j+1)
			}
		}
	}
	if maxSim > threshold.MaxNearDuplicateScore {
		return doctorCheck{
			ID:      "semantic_duplicate",
			Level:   "warn",
			Message: fmt.Sprintf("片段语义重复度偏高（max_sim=%.2f, pair=%s）", roundMillis(maxSim), maxPair),
			Details: map[string]interface{}{
				"max_similarity": roundMillis(maxSim),
				"pair":           maxPair,
				"threshold":      threshold.MaxNearDuplicateScore,
			},
		}
	}
	return doctorCheck{
		ID:      "semantic_duplicate",
		Level:   "pass",
		Message: fmt.Sprintf("片段语义重复度可接受（max_sim=%.2f）", roundMillis(maxSim)),
		Details: map[string]interface{}{
			"max_similarity": roundMillis(maxSim),
			"threshold":      threshold.MaxNearDuplicateScore,
		},
	}
}

func doctorCheckUniformPattern(clips []prepClip) doctorCheck {
	if len(clips) < 3 {
		return doctorCheck{
			ID:      "uniform_sampling_pattern",
			Level:   "pass",
			Message: "片段数较少，跳过均匀采样模式检测",
		}
	}
	durs := make([]float64, 0, len(clips))
	starts := make([]float64, 0, len(clips))
	for _, c := range clips {
		d := c.DurationSec
		if d <= 0 && c.EndSec > c.StartSec {
			d = c.EndSec - c.StartSec
		}
		durs = append(durs, d)
		starts = append(starts, c.StartSec)
	}
	sort.Float64s(starts)
	gaps := make([]float64, 0, len(starts)-1)
	for i := 1; i < len(starts); i++ {
		gaps = append(gaps, starts[i]-starts[i-1])
	}

	durStd := doctorStd(durs)
	gapStd := doctorStd(gaps)
	durMean := doctorMean(durs)
	gapMean := doctorMean(gaps)

	almostEqualDur := durMean > 0 && durStd <= math.Max(0.8, durMean*0.06)
	almostEqualGap := gapMean > 0 && gapStd <= math.Max(1.2, gapMean*0.10)
	if almostEqualDur && almostEqualGap {
		return doctorCheck{
			ID:      "uniform_sampling_pattern",
			Level:   "warn",
			Message: "clips 疑似均匀采样模式。若目标是语义高光，建议使用“AI 候选 + 人工决策”后再导出",
			Details: map[string]interface{}{
				"duration_std": roundMillis(durStd),
				"gap_std":      roundMillis(gapStd),
			},
		}
	}
	return doctorCheck{
		ID:      "uniform_sampling_pattern",
		Level:   "pass",
		Message: "未检测到明显的均匀采样模式",
		Details: map[string]interface{}{
			"duration_std": roundMillis(durStd),
			"gap_std":      roundMillis(gapStd),
		},
	}
}

func loadDoctorSubtitle(plan prepPlan) ([]subtitleCue, string, bool) {
	subtitlePath := strings.TrimSpace(plan.Outputs.SubtitlePath)
	hasReal := subtitlePath != "" && fileExists(subtitlePath)
	if !hasReal {
		subtitlePath = strings.TrimSpace(plan.Outputs.SubtitleTemplate)
	}
	if subtitlePath == "" || !fileExists(subtitlePath) {
		return nil, subtitlePath, false
	}
	cues, err := parseSubtitleCues(subtitlePath)
	if err != nil {
		return nil, subtitlePath, hasReal
	}
	if strings.Contains(strings.ToLower(filepath.Base(subtitlePath)), "template") {
		hasReal = false
	}
	return cues, subtitlePath, hasReal
}

func doctorClipSubtitleCoverage(c prepClip, cues []subtitleCue) float64 {
	clipDur := c.DurationSec
	if clipDur <= 0 && c.EndSec > c.StartSec {
		clipDur = c.EndSec - c.StartSec
	}
	if clipDur <= 0 {
		return 0
	}

	covered := 0.0
	for _, cue := range cues {
		inter := doctorIntersectionLen(c.StartSec, c.EndSec, cue.StartSec, cue.EndSec)
		if inter > 0 {
			covered += inter
		}
	}
	coverage := covered / clipDur
	if coverage > 1 {
		return 1
	}
	if coverage < 0 {
		return 0
	}
	return coverage
}

func doctorIntersectionLen(aStart, aEnd, bStart, bEnd float64) float64 {
	start := math.Max(aStart, bStart)
	end := math.Min(aEnd, bEnd)
	if end <= start {
		return 0
	}
	return end - start
}

func doctorOverlapRatio(a, b prepClip) float64 {
	inter := doctorIntersectionLen(a.StartSec, a.EndSec, b.StartSec, b.EndSec)
	if inter <= 0 {
		return 0
	}
	aDur := a.DurationSec
	if aDur <= 0 && a.EndSec > a.StartSec {
		aDur = a.EndSec - a.StartSec
	}
	bDur := b.DurationSec
	if bDur <= 0 && b.EndSec > b.StartSec {
		bDur = b.EndSec - b.StartSec
	}
	base := math.Min(aDur, bDur)
	if base <= 0 {
		return 0
	}
	return inter / base
}

func doctorCutBoundary(t float64, cues []subtitleCue, isStart bool) bool {
	const margin = 0.30
	for _, cue := range cues {
		if t < cue.StartSec || t > cue.EndSec {
			continue
		}
		if isStart {
			return t > cue.StartSec+margin
		}
		return t < cue.EndSec-margin
	}
	return false
}

func doctorClipText(c prepClip, cues []subtitleCue) string {
	parts := make([]string, 0, 8)
	for _, cue := range cues {
		if doctorIntersectionLen(c.StartSec, c.EndSec, cue.StartSec, cue.EndSec) <= 0 {
			continue
		}
		t := strings.TrimSpace(cue.Text)
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

func doctorJaccardSimilarity(a, b string) float64 {
	aSet := doctorTokenSet(a)
	bSet := doctorTokenSet(b)
	if len(aSet) == 0 || len(bSet) == 0 {
		return 0
	}
	inter := 0
	for t := range aSet {
		if _, ok := bSet[t]; ok {
			inter++
		}
	}
	union := len(aSet) + len(bSet) - inter
	if union <= 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func doctorTokenSet(s string) map[string]struct{} {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		return true
	})
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if _, err := strconv.Atoi(w); err == nil {
			continue
		}
		set[w] = struct{}{}
	}
	return set
}

func doctorClipLabel(c prepClip, idx int) string {
	label := strings.TrimSpace(c.Label)
	if label != "" {
		return label
	}
	return fmt.Sprintf("clip-%02d", idx+1)
}

func doctorMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func doctorStd(values []float64) float64 {
	if len(values) <= 1 {
		return 0
	}
	mean := doctorMean(values)
	acc := 0.0
	for _, v := range values {
		d := v - mean
		acc += d * d
	}
	return math.Sqrt(acc / float64(len(values)))
}

func doctorMin(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

func summarizeDoctorChecks(checks []doctorCheck) doctorSummary {
	s := doctorSummary{Total: len(checks)}
	for _, c := range checks {
		switch c.Level {
		case "pass":
			s.Pass++
		case "warn":
			s.Warn++
		case "fail":
			s.Fail++
		}
	}
	return s
}

func doctorExitWithErr(asJSON bool, exitCode int, msg string) int {
	if asJSON {
		printDoctorJSON(doctorJSONResult{
			OK:       false,
			ExitCode: exitCode,
			Error:    msg,
		})
	} else {
		logError("doctor.failed", "exit_code", exitCode, "detail", msg)
	}
	return exitCode
}

func printDoctorJSON(v doctorJSONResult) {
	data, err := json.Marshal(v)
	if err != nil {
		logError("json.marshal_failed", "context", "doctor_result", "error", err)
		return
	}
	fmt.Println(string(data))
}
