// Package prompts embeds all AI system prompts at compile time.
//
// Why a separate package? Go's //go:embed directive cannot reach
// outside its own directory. By collecting prompts here and exporting
// them as constants, any agent can import what it needs without
// every agent re-embedding files.
//
// Treat prompt files as product artifacts: edit them in any text
// editor, ship them in code review like docs, and re-run evals after
// changes. They are the single biggest lever for AI output quality.
package prompts

import _ "embed"

//go:embed receipt_scan.txt
var ReceiptScan string
