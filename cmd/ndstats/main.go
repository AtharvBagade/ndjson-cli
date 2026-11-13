package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
)

type configFlags struct {
    groupField string
    valueField string
}

type groupStat struct {
	count        int     // records in this group
	skippedCount int     // records whose -field value was missing or unusable
	valueCount   int     // records with a usable -field value
	sum          float64 // running total of values, for the mean
	min          float64 // only valid when valueCount > 0
	max          float64 // only valid when valueCount > 0
}

func (g *groupStat) getGroupStats() int {
	return g.count
}

func (g *groupStat) incrementCount() {
	g.count++
}

func (g *groupStat) incrementSkippedCount() {
	g.skippedCount++
}

func (g *groupStat) addValue(v float64) {
	if g.valueCount == 0 || v < g.min {
		g.min = v
	}
	if g.valueCount == 0 || v > g.max {
		g.max = v
	}
	g.sum += v
	g.valueCount++
}

func (g *groupStat) mean() float64 {
	return g.sum / float64(g.valueCount)
}

func normaliseGroupKeys(groups map[string]*groupStat) []string {
	return slices.SortedFunc(maps.Keys(groups), func(a, b string) int {
		countA := groups[a].getGroupStats()
		countB := groups[b].getGroupStats()
		if c := cmp.Compare(countB, countA); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
}

func printFinalStats(groups map[string]*groupStat, groupField string, totalLines, totalLinesSkipped int) {
	fmt.Printf("%-12s %7s %7s %8s %10s %10s %10s\n", groupField, "count", "values", "skipped", "min", "max", "mean")
	for _, key := range normaliseGroupKeys(groups) {
		stat := groups[key]
		if stat.valueCount == 0 {
			// No usable values: min, max and mean are undefined, not 0.
			fmt.Printf("%-12s %7d %7d %8d %10s %10s %10s\n", key, stat.count, stat.valueCount, stat.skippedCount, "-", "-", "-")
			continue
		}
		fmt.Printf("%-12s %7d %7d %8d %10.2f %10.2f %10.2f\n", key, stat.count, stat.valueCount, stat.skippedCount, stat.min, stat.max, stat.mean())
	}
	fmt.Printf("Total lines processed: %d\n", totalLines)
	fmt.Printf("Lines skipped: %d\n", totalLinesSkipped)
}

func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}

func toString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	}
	return "", false
}

func parseConfig() *configFlags {
	group := flag.String("group", "", "field to group records by")
	field := flag.String("field", "", "field to calculate statistics for")
	flag.Parse()

    missing := []string{}
    if *group == "" {
        missing = append(missing, "-group")
    }
    if *field == "" {
        missing = append(missing, "-field")
    }
    if len(missing) > 0 {
        fmt.Fprintf(os.Stderr, "Missing required flags: %v\n", missing)
        flag.Usage()
        os.Exit(2)
    }

	return &configFlags{
		groupField: *group,
		valueField: *field,
	}
}

func main() {

	config := parseConfig()

    group := config.groupField
    field := config.valueField

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	groups := make(map[string]*groupStat)

	totalLines := 0
	totalLinesSkipped := 0

	for scanner.Scan() {
		totalLines++
		text := scanner.Text()
		var jsonValue map[string]any
		err := json.Unmarshal([]byte(text), &jsonValue)
		if err != nil {
			log.Printf("Error parsing JSON: %v at line %d", err, totalLines)
			totalLinesSkipped++
			continue
		}
		raw, ok := jsonValue[group]
		if !ok {
			log.Printf("Group key '%s' not found : %v at line %d", group, jsonValue, totalLines)
			totalLinesSkipped++
			continue
		}

		groupKey, ok := toString(raw)
		if !ok {
			log.Printf("Not a string or number for group key '%s' : %v at line %d", group, raw, totalLines)
			totalLinesSkipped++
			continue
		}

		stat, exists := groups[groupKey]
		if !exists {
			stat = &groupStat{}
			groups[groupKey] = stat
		}
		stat.incrementCount()

		value, ok := toFloat64(jsonValue[field])
		if !ok {
			stat.incrementSkippedCount()
			continue
		}
		stat.addValue(value)
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	printFinalStats(groups, group, totalLines, totalLinesSkipped)
}
