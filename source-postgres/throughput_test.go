package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/estuary/connectors/sqlcapture"
	"github.com/estuary/connectors/sqlcapture/tests"
	"github.com/estuary/flow/go/protocols/airbyte"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// To run these benchmarks:
//
//     $ LOG_LEVEL=warn go test -run=NoTests -bench=. -benchtime=120s ./source-postgres/
//     $ LOG_LEVEL=warn go test -run=NoTests -bench=. -benchtime=120s -memprofile memprofile.out -cpuprofile profile.out ./source-postgres/
//

// The benchmarks 'BenchmarkBackfill' and 'BenchmarkReplication' initialize
// several tables with a bunch of synthetic data and benchmark how long it
// takes to capture the data (via Backfill and Replication, respectively).
//
// In both cases the tests will run 10 capture iterations and spread the
// data across 10 tables, with the benchmark 'N' parameter controlling the
// number of rows in each table. However the number of rows is 'N/100' so
// that the 'ns/op' measurement will reflect the marginal cost of a single
// row-capture via each mechanism.
//
// These benchmarks have a rather high setup cost, so the default `benchtime`
// target of one second isn't nearly enough. Aim for at least 30 seconds to
// get useful numbers (and 300 seconds for perfect accuracy):
//
//     $ LOG_LEVEL=warn go test -run=NoTests -bench=. -benchtime=30s ./source-postgres/
//     $ LOG_LEVEL=warn go test -run=NoTests -bench=. -benchtime=30s -memprofile memprofile.out -cpuprofile profile.out ./source-postgres/
//
func BenchmarkBackfill(b *testing.B) { benchmarkBackfills(b, 10, 10, b.N/100) }

// This benchmark varies the row count with increasing N, so the 'ns/op'
// value directly reflects the marginal time taken to capture one row.
func BenchmarkReplication(b *testing.B) { benchmarkReplication(b, 10, 10, b.N/100) }

func benchmarkBackfills(b *testing.B, iterations, numTables, rowsPerTable int) {
	b.StopTimer()
	b.ResetTimer()

	// Set up multiple tables full of data
	logrus.WithFields(logrus.Fields{
		"tables":       numTables,
		"rowsPerTable": rowsPerTable,
	}).Info("initializing tables")

	var tb, ctx = TestBackend, context.Background()
	var tables []string
	for i := 0; i < numTables; i++ {
		var table = tb.CreateTable(ctx, b, fmt.Sprintf("table%d", i), "(id INTEGER PRIMARY KEY, uid TEXT, name TEXT, status INTEGER, modified DATE, foo_id INTEGER, padding TEXT)")
		tables = append(tables, table)
		populateTable(ctx, b, tb, table, rowsPerTable)
	}
	var catalog = tests.ConfiguredCatalog(ctx, b, tb, tables...)
	var dummyOutput = &benchmarkMessageOutput{Inner: json.NewEncoder(io.Discard)}

	logrus.WithField("iterations", iterations).Info("running backfill benchmark")
	for i := 0; i < iterations; i++ {
		var state = sqlcapture.PersistentState{}
		b.StartTimer()
		if err := sqlcapture.RunCapture(ctx, tb.GetDatabase(), &catalog, &state, dummyOutput); err != nil {
			b.Fatalf("capture failed with error: %v", err)
		}
		b.StopTimer()
		var expectedRecords = numTables * rowsPerTable
		if dummyOutput.Total != expectedRecords {
			b.Fatalf("incorrect document count: got %d, expected %d", dummyOutput.Total, expectedRecords)
		}
		dummyOutput.Reset()
	}
}

func benchmarkReplication(b *testing.B, iterations, numTables, rowsPerTable int) {
	b.StopTimer()
	b.ResetTimer()

	var tb, ctx = TestBackend, context.Background()
	var tables []string
	for i := 0; i < numTables; i++ {
		var table = tb.CreateTable(ctx, b, fmt.Sprintf("table%d", i), "(id INTEGER PRIMARY KEY, uid TEXT, name TEXT, status INTEGER, modified DATE, foo_id INTEGER, padding TEXT)")
		tables = append(tables, table)
	}
	var catalog = tests.ConfiguredCatalog(ctx, b, tb, tables...)
	var dummyOutput = &benchmarkMessageOutput{Inner: json.NewEncoder(io.Discard)}

	var initialState = sqlcapture.PersistentState{}
	if err := sqlcapture.RunCapture(ctx, tb.GetDatabase(), &catalog, &initialState, dummyOutput); err != nil {
		b.Fatalf("capture failed with error: %v", err)
	}

	for _, table := range tables {
		populateTable(ctx, b, tb, table, rowsPerTable)
	}

	for i := 0; i < iterations; i++ {
		var state = tests.CopyState(initialState)
		b.StartTimer()
		if err := sqlcapture.RunCapture(ctx, tb.GetDatabase(), &catalog, &state, dummyOutput); err != nil {
			b.Fatalf("capture failed with error: %v", err)
		}
		b.StopTimer()
		var expectedRecords = numTables * rowsPerTable
		if dummyOutput.Total != expectedRecords {
			b.Fatalf("incorrect document count: got %d, expected %d", dummyOutput.Total, expectedRecords)
		}
		dummyOutput.Reset()
	}
}

func populateTable(ctx context.Context, t testing.TB, tb tests.TestBackend, table string, numRows int) {
	t.Helper()

	const chunkSize = 4096

	var buffer [][]interface{}
	for i := 0; i < numRows; i++ {
		var date = time.Unix(683640000+rand.Int63n(974764800), 0).Format("2006-01-02")
		var padding = strings.Repeat("PADDING.", rand.Intn(33))
		buffer = append(buffer, []interface{}{
			// Total size: 192 +/- 132 bytes per row
			i,                           // (4) Serial Integer Primary Key
			uuid.New().String(),         // (36) Random UUID as a string
			fmt.Sprintf("Thing #%d", i), // (8-16) Human readable name
			100 + rand.Intn(400),        // (4) Integer status code
			date,                        // (4) Random YYYY-MM-DD date within the past 30 years
			rand.Int31(),                // (4) Random integer ID
			padding,                     // (0-256) Variable amount of padding
		})
		if len(buffer) >= chunkSize {
			tb.Insert(ctx, t, table, buffer)
			buffer = nil
		}
	}
	if len(buffer) > 0 {
		tb.Insert(ctx, t, table, buffer)
	}
}

type benchmarkMessageOutput struct {
	Total int
	Inner sqlcapture.MessageOutput
}

func (m *benchmarkMessageOutput) Reset() {
	m.Total = 0
}

func (m *benchmarkMessageOutput) Encode(v interface{}) error {
	if msg, ok := v.(airbyte.Message); ok && msg.Type == airbyte.MessageTypeRecord {
		m.Total++
	}
	return m.Inner.Encode(v)
}