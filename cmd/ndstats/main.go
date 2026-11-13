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

type groupStat struct {
	count        int
	skippedCount int
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

func printFinalStats(groups map[string]*groupStat, totalLines, totalLinesSkipped int) {
	for _, key := range normaliseGroupKeys(groups) {
		count := groups[key].getGroupStats()
		fmt.Printf("%s: %d \n", key, count)
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

func main() {

	group := flag.String("group", "", "field to group records by")
    field := flag.String("field", "", "field to calculate statistics for")
	flag.Parse()

	if *group == "" || *field == "" {
		flag.Usage()
		os.Exit(2)
	}

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
		raw, ok := jsonValue[*group]
		if !ok {
			log.Printf("Group key '%s' not found : %v at line %d", *group, jsonValue, totalLines)
			totalLinesSkipped++
			continue
		}

		groupKey, ok := toString(raw)
		if !ok {
			log.Printf("Not a string or number for group key '%s' : %v at line %d", *group, raw, totalLines)
			totalLinesSkipped++
			continue
		}

		stat, exists := groups[groupKey]
		if !exists {
			stat = &groupStat{}
			groups[groupKey] = stat
		}
		stat.incrementCount()
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	printFinalStats(groups, totalLines, totalLinesSkipped)
}
