// Package transferwords is what an owner transfer says about the files a
// script's runs created (#1588): which of them a transfer is about, how many
// there are, and the sentence that tells an administrator whether they moved
// with the script or stayed where they were.
//
// It reads the producer relation's rows and addresses and nothing else, so the
// wording can be read and tested without the handler that answers the transfer
// and cannot import it back.
package transferwords

import (
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/producedview"
)

// Created narrows what a script wrote to what a transfer is about: the live
// assets and collections its runs CREATED. A file the script only wrote a
// version over is somebody else's and is not the script's to move; a resource
// is filed by library rather than by address and has no owner to change; a
// deleted file is gone either way.
func Created(items []producedview.Item) []producedview.Item {
	out := make([]producedview.Item, 0, len(items))
	for _, it := range items {
		switch it.TargetKind {
		case producedview.TargetAsset, producedview.TargetCollection:
		default:
			continue
		}
		if it.Created && !it.Deleted {
			out = append(out, it)
		}
	}
	return out
}

// Account is what a transfer did with a script's outputs, in the terms its
// sentence needs: whether they moved, how many of each kind, and, for ones
// that stayed, the address each stayed with.
type Account struct {
	Moved       bool
	Assets      int
	Collections int
	KeptWith    []string
}

// Sentence states, after the sentence about the script, what became of its
// outputs. It is the answer to the question the ticket found nobody was asked
// (#1588): a transfer moves the automation, and whether the files it
// refreshes went with it is the part an administrator is most likely to be
// wrong about.
func Sentence(a Account, owner string) string {
	files := Counts(a.Assets, a.Collections)
	if a.Moved {
		return " The " + files + " its runs wrote now belong to " + owner + " too."
	}
	if len(a.KeptWith) == 0 {
		return " The " + files + " its runs wrote already belong to " + owner + "."
	}
	return " The " + files + " its runs wrote stay with " + keptOwners(a.KeptWith) + ". " +
		owner + " cannot open, share or delete them, and each run goes on writing a new version into them."
}

// keptOwners names who the kept outputs stayed with: one address when they
// share one, and the plain fact when they do not.
func keptOwners(kept []string) string {
	owner := kept[0]
	for _, it := range kept[1:] {
		if !strings.EqualFold(it, owner) {
			return "their current owners"
		}
	}
	if owner == "" {
		return "nobody"
	}
	return owner
}

// KeptWith names who the outputs stay with when they are not moved, for the
// refusal that asks the caller to choose: the script's owner when every row
// names them, and the plain fact otherwise.
func KeptWith(created []producedview.Item, scriptOwner string) string {
	for _, it := range created {
		if !SameAddress(it.OwnerEmail, scriptOwner) {
			return "their current owners"
		}
	}
	return scriptOwner
}

// SameAddress reports whether a row's address names the person at owner. The
// comparison is case-insensitive, as every address comparison in the platform
// is, and an unattributed row names nobody.
func SameAddress(rowOwner, owner string) bool {
	return rowOwner != "" && strings.EqualFold(rowOwner, owner)
}

// Count renders "2 assets and 1 collection" for a set of outputs.
func Count(created []producedview.Item) string {
	var assets, collections int
	for _, it := range created {
		if it.TargetKind == producedview.TargetAsset {
			assets++
		} else {
			collections++
		}
	}
	return Counts(assets, collections)
}

// Counts renders the two counts as prose, naming only the kinds that are
// present.
func Counts(assets, collections int) string {
	parts := make([]string, 0, 2)
	if assets > 0 {
		parts = append(parts, plural(assets, "asset"))
	}
	if collections > 0 {
		parts = append(parts, plural(collections, "collection"))
	}
	return strings.Join(parts, " and ")
}

// plural renders a count with its noun.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
