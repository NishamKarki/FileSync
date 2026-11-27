// Command benchreport summarizes FileSync transfer and conflict CSV measurements.
package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type summary struct {
	fileSize, editLocation, chunkSize, mode           string
	chunkBytes, savings, chunkRatio                   []float64
	prepare, transfer, total, cpu, storage, detection []float64
}

func main() {
	directory := os.Getenv("FILESYNC_PERFORMANCE_LOG_DIR")
	if directory == "" {
		directory = "Performance Logs"
	}
	summaries, loadingError := loadSummaries(directory)
	if loadingError != nil {
		fail(loadingError)
	}
	if writingError := writeSummaries(directory, summaries); writingError != nil {
		fail(writingError)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "benchreport:", err); os.Exit(1) }

// loadSummaries reads the logs without changing their raw evidence.
func loadSummaries(directory string) (map[string]*summary, error) {
	summaries := make(map[string]*summary)
	if err := readSyncMetrics(filepath.Join(directory, "sync_metrics.csv"), summaries); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := readConflictMetrics(filepath.Join(directory, "conflict_metrics.csv"), summaries); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return summaries, nil
}

func readSyncMetrics(path string, summaries map[string]*summary) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return err
	}
	for _, row := range rows[1:] {
		if len(row) < 15 || row[14] == "failed" {
			continue
		}
		size, edit := scenario(row[1], row[3])
		item := group(summaries, size, edit, row[4], row[2])
		item.chunkBytes = append(item.chunkBytes, number(row[7]))
		item.savings = append(item.savings, number(row[8]))
		item.chunkRatio = append(item.chunkRatio, ratio(number(row[6]), number(row[5])))
		item.prepare = append(item.prepare, number(row[9]))
		item.transfer = append(item.transfer, number(row[10]))
		item.total = append(item.total, number(row[11]))
		item.cpu = append(item.cpu, number(row[12]))
		item.storage = append(item.storage, number(row[13]))
	}
	return nil
}

func readConflictMetrics(path string, summaries map[string]*summary) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return err
	}
	for _, row := range rows[1:] {
		if len(row) < 5 {
			continue
		}
		size, edit := scenario(row[1], row[2])
		for _, item := range summaries {
			if item.fileSize == size && item.editLocation == edit {
				item.detection = append(item.detection, number(row[4]))
			}
		}
		if len(summaries) == 0 {
			for _, mode := range []string{"incremental", "whole-file"} {
				group(summaries, size, edit, "0", mode).detection = append(group(summaries, size, edit, "0", mode).detection, number(row[4]))
			}
		}
	}
	return nil
}

// scenario parses standard benchmark names: bench_4MB_middle_trial1.bin.
func scenario(fileName, fallbackSize string) (string, string) {
	parts := strings.Split(strings.TrimSuffix(fileName, filepath.Ext(fileName)), "_")
	if len(parts) >= 3 && parts[0] == "bench" {
		return parts[1], parts[2]
	}
	return fallbackSize, "unspecified"
}

func group(all map[string]*summary, size, edit, chunkSize, mode string) *summary {
	key := strings.Join([]string{size, edit, chunkSize, mode}, "|")
	if all[key] == nil {
		all[key] = &summary{fileSize: size, editLocation: edit, chunkSize: chunkSize, mode: mode}
	}
	return all[key]
}

func writeSummaries(directory string, summaries map[string]*summary) error {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	items := make([]*summary, 0, len(summaries))
	for _, item := range summaries {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		a := items[i].fileSize + "|" + items[i].chunkSize + "|" + items[i].editLocation + "|" + items[i].mode
		b := items[j].fileSize + "|" + items[j].chunkSize + "|" + items[j].editLocation + "|" + items[j].mode
		return a < b
	})
	file, err := os.Create(filepath.Join(directory, "benchmark_summary.csv"))
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	header := []string{"file_size", "edit_location", "chunk_size_bytes", "mode", "mean_chunk_bytes_sent", "median_chunk_bytes_sent", "mean_bytes_saved_percent", "mean_chunks_sent_ratio", "mean_prepare_ms", "mean_transfer_ms", "mean_total_ms", "mean_cpu_ms", "mean_chunk_storage_bytes", "mean_conflict_detection_ms"}
	if err := writer.Write(header); err != nil {
		return err
	}
	markdown := []string{"# FileSync Benchmark Summary", "", "| File size | Edit location | Chunk size | Mode | Mean chunk bytes | Median chunk bytes | Mean saved | Mean chunk ratio | Mean total ms | Mean conflict detection ms |", "|---|---|---:|---|---:|---:|---:|---:|---:|---:|"}
	for _, item := range items {
		row := []string{item.fileSize, item.editLocation, item.chunkSize, item.mode, format(mean(item.chunkBytes)), format(median(item.chunkBytes)), format(mean(item.savings)), format(mean(item.chunkRatio)), format(mean(item.prepare)), format(mean(item.transfer)), format(mean(item.total)), format(mean(item.cpu)), format(mean(item.storage)), format(mean(item.detection))}
		if err := writer.Write(row); err != nil {
			return err
		}
		markdown = append(markdown, fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s%% | %s | %s | %s |", item.fileSize, item.editLocation, item.chunkSize, item.mode, row[4], row[5], row[6], row[7], row[10], row[13]))
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "benchmark_summary.md"), []byte(strings.Join(markdown, "\n")+"\n"), 0644)
}

func number(value string) float64 { result, _ := strconv.ParseFloat(value, 64); return result }
func ratio(top, bottom float64) float64 {
	if bottom == 0 {
		return 0
	}
	return top / bottom
}
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 0 {
		return (copyValues[middle-1] + copyValues[middle]) / 2
	}
	return copyValues[middle]
}
func format(value float64) string { return strconv.FormatFloat(value, 'f', 3, 64) }
