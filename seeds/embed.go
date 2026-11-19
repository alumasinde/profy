// Package seeds embeds idempotent reference-data seed files, applied in name order.
package seeds

import "embed"

// FS holds the seed files.
//
//go:embed *.sql
var FS embed.FS
