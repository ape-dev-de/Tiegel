// Package diffsample provides sample data for the diff organisms (catalog
// and tests): two versions of a PHP class, a blame and a file history.
package diffsample

import (
	"fmt"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff"
)

// Old is the base version of the sample file.
const Old = `<?php
declare(strict_types=1);

namespace App\Billing;

use App\Billing\Tax\TaxRate;

final class InvoiceCalculator
{
    private const VAT = 0.19;

    public function __construct(
        private InvoiceRepository $invoices,
    ) {
    }

    public function total(Invoice $invoice): float
    {
        $net = 0.0;
        foreach ($invoice->lines() as $line) {
            $net += $line->price * $line->quantity;
        }
        return round($net * (1 + self::VAT), 2);
    }

    public function legacyTotal(Invoice $invoice): float
    {
        return $this->total($invoice);
    }

    public function currency(): string
    {
        return 'EUR';
    }
}
`

// New is the changed version of the sample file.
const New = `<?php
declare(strict_types=1);

namespace App\Billing;

use App\Billing\Tax\TaxRate;
use App\Billing\Tax\TaxPolicy;

final class InvoiceCalculator
{
    private const VAT = 0.19;

    public function __construct(
        private readonly InvoiceRepository $invoices,
        private readonly TaxPolicy $policy,
    ) {
    }

    public function total(Invoice $invoice): float
    {
        $net = 0.0;
        foreach ($invoice->lines() as $line) {
            $net += $line->unitPrice * $line->quantity;
        }
        $rate = $this->policy->rateFor($invoice);
        return round($net * (1 + $rate), 2);
    }

    public function currency(): string
    {
        return 'EUR';
    }
}
`

// Path is the sample file path.
const Path = "src/Billing/InvoiceCalculator.php"

// Content returns the sample comparison HEAD ↔ work tree.
func Content() diff.Content {
	return diff.Content{
		Left:   diff.Side{Label: "HEAD (4f2a9c1e)", Path: Path, Text: Old},
		Right:  diff.Side{Label: "Working tree", Path: Path, Text: New},
		Blocks: diff.Compute(Old, New, false),
	}
}

// Large returns two versions of a generated file with n lines and a change
// every step lines (for performance tests).
func Large(n, step int) (left, right string) {
	var l, r strings.Builder
	l.WriteString("<?php\n")
	r.WriteString("<?php\n")
	for i := 1; i < n; i++ {
		line := fmt.Sprintf("$value%d = compute($input, %d); // %s\n", i, i, strings.Repeat("x", i%40))
		l.WriteString(line)
		switch {
		case i%step == 0:
			fmt.Fprintf(&r, "$value%d = compute($input, %d + 1); // changed\n", i, i)
		case i%step == step/2:
			// deleted on the right
		case i%step == step/3:
			r.WriteString(line)
			fmt.Fprintf(&r, "$extra%d = true;\n", i)
		default:
			r.WriteString(line)
		}
	}
	return l.String(), r.String()
}

var base = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

// Blame returns sample annotations for New: commits and the hash of every
// line.
func Blame() ([]diff.BlameCommit, []string) {
	commits := []diff.BlameCommit{
		{Hash: "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", Author: "Mara Lindqvist", Date: base, Summary: "Add invoice calculator"},
		{Hash: "b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1", Author: "Jonas Weber", Date: base.AddDate(0, 2, 3), Summary: "Use readonly promoted properties"},
		{Hash: "c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2", Author: "Aiko Tanaka", Date: base.AddDate(0, 5, 20), Summary: "Tax rate from policy"},
		{Hash: gitcli.UncommittedHash, Author: "Not Committed Yet", Uncommitted: true},
	}
	n := strings.Count(New, "\n")
	hashes := make([]string, n)
	for i := range hashes {
		switch {
		case i == 6 || i == 14 || i == 24 || i == 25:
			hashes[i] = commits[2].Hash
		case i == 13:
			hashes[i] = commits[1].Hash
		case i == 22:
			hashes[i] = commits[3].Hash
		default:
			hashes[i] = commits[0].Hash
		}
	}
	return commits, hashes
}

// History returns n sample history entries of Path (newest first) with a
// rename at the oldest third.
func History(skip, n int) []gitcli.FileCommit {
	authors := []string{"Mara Lindqvist", "Jonas Weber", "Aiko Tanaka", "Lena Novak"}
	subjects := []string{"Tax rate from policy", "Use readonly promoted properties", "Round totals to cents",
		"Extract currency", "Fix quantity type", "Add invoice calculator"}
	out := make([]gitcli.FileCommit, 0, n)
	for i := skip; i < skip+n; i++ {
		hash, parent := fakeHash(i), fakeHash(i+1)
		c := gitcli.FileCommit{
			Commit: gitcli.Commit{Hash: hash, Parents: []string{parent}, AuthorName: authors[i%len(authors)],
				AuthorDate: base.AddDate(0, 0, -i*3), Subject: subjects[i%len(subjects)]},
			Path: Path, Status: 'M',
		}
		if i == 0 {
			c.Refs = []string{"HEAD -> main"}
		}
		out = append(out, c)
	}
	return out
}

// fakeHash returns a stable, random looking 40 digit hex hash for n.
func fakeHash(n int) string {
	var b strings.Builder
	x := uint32(n)*2654435761 + 0x9e3779b9
	for i := 0; i < 5; i++ {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		fmt.Fprintf(&b, "%08x", x)
	}
	return b.String()
}
