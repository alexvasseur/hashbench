package workload

import (
	"fmt"
	"math/rand"
)

type OpType int

const (
	OpRead OpType = iota
	OpWrite
)

type Generator struct {
	keys       int
	valueBytes int
	writeRatio float64
	keyPattern string
	seq        int
	rng        *rand.Rand
}

func NewGenerator(keys, valueBytes int, writeRatio float64, keyPattern string, rng *rand.Rand) *Generator {
	return &Generator{
		keys:       keys,
		valueBytes: valueBytes,
		writeRatio: writeRatio,
		keyPattern: keyPattern,
		rng:        rng,
	}
}

func (g *Generator) NextOpType() OpType {
	if g.rng.Float64() < g.writeRatio {
		return OpWrite
	}
	return OpRead
}

func (g *Generator) NextKey(fieldCount int) string {
	var idx int
	if g.keyPattern == "sequential" {
		idx = g.seq % g.keys
		g.seq++
	} else {
		idx = g.rng.Intn(g.keys)
	}
	return fmt.Sprintf("h%d:%d", fieldCount, idx)
}

func (g *Generator) NextIndex() int {
	if g.keyPattern == "sequential" {
		idx := g.seq % g.keys
		g.seq++
		return idx
	}
	return g.rng.Intn(g.keys)
}

func KeyForIndex(fieldCount int, idx int) string {
	return fmt.Sprintf("h%d:%d", fieldCount, idx)
}

func (g *Generator) NextFieldCount() int {
	// Weighted distribution:
	// 35% -> 5 fields, 50% -> 10 fields, 10% -> 20 fields, 5% -> 100 fields.
	n := g.rng.Intn(100)
	switch {
	case n < 35:
		return 5
	case n < 85:
		return 10
	case n < 95:
		return 20
	default:
		return 100
	}
}

func FieldCountForIndex(idx int) int {
	n := idx % 100
	switch {
	case n < 35:
		return 5
	case n < 85:
		return 10
	case n < 95:
		return 20
	default:
		return 100
	}
}

func (g *Generator) Fields(n int) []string {
	fields := make([]string, n)
	for i := 0; i < n; i++ {
		fields[i] = fmt.Sprintf("f%d", i)
	}
	return fields
}

func (g *Generator) HSetArgs(n int) []interface{} {
	args := make([]interface{}, 0, n*2)
	for i := 0; i < n; i++ {
		field := fmt.Sprintf("f%d", i)
		val := make([]byte, g.valueBytes)
		_, _ = g.rng.Read(val)
		args = append(args, field, val)
	}
	return args
}

func NewRNG(baseSeed int64, workerID int) *rand.Rand {
	seed := baseSeed + int64(workerID)*9973
	return rand.New(rand.NewSource(seed))
}
