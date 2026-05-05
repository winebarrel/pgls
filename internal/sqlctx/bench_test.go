package sqlctx

import "testing"

const benchSQL = `SELECT u.id, u.email, o.total
FROM users u
JOIN orders o ON u.id = o.user_id
WHERE u.email IS NOT NULL AND o.total > 100
ORDER BY o.created_at DESC
LIMIT 50`

func BenchmarkTokenizeRegex(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = tokenizeRegex(benchSQL)
	}
}

func BenchmarkTokenizeScan(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = tokenize(benchSQL) // calls pg_query.Scan
	}
}

func BenchmarkAnalyze(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Analyze(benchSQL, len(benchSQL))
	}
}

func BenchmarkLint(b *testing.B) {
	s := makeSchema()
	for i := 0; i < b.N; i++ {
		_ = Lint(benchSQL, s)
	}
}
