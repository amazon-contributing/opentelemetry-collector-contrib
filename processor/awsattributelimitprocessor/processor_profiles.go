// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsattributelimitprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsattributelimitprocessor"

import (
	"context"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

// profileAttrLeaf adapts a profile's dictionary-encoded attributes to the
// leafAttributes interface. In the OTLP profiles data model a profile's
// attributes are references (indices) into the shared ProfilesDictionary
// attribute table rather than an inline map. This adapter resolves keys through
// that table but only ever mutates the per-profile index list -- it never
// mutates the shared attribute or string tables, which are referenced by other
// profiles, samples, and locations. Dropping an attribute therefore means
// removing its index from this profile, not deleting the shared table entry.
type profileAttrLeaf struct {
	indices pcommon.Int32Slice
	table   pprofile.KeyValueAndUnitSlice
	strings pcommon.StringSlice
}

// keyAt resolves the attribute key for a table index, guarding against
// out-of-range indices from malformed input (returns "" rather than panicking).
func (l profileAttrLeaf) keyAt(tableIdx int32) string {
	i := int(tableIdx)
	if i < 0 || i >= l.table.Len() {
		return ""
	}
	keyIdx := int(l.table.At(i).KeyStrindex())
	if keyIdx < 0 || keyIdx >= l.strings.Len() {
		return ""
	}
	return l.strings.At(keyIdx)
}

func (l profileAttrLeaf) Len() int { return l.indices.Len() }

func (l profileAttrLeaf) rangeKeys(f func(string) bool) {
	for i := 0; i < l.indices.Len(); i++ {
		if !f(l.keyAt(l.indices.At(i))) {
			return
		}
	}
}

// remove deletes the first index whose resolved key matches, so a single call
// drops exactly one attribute occurrence (mirroring one map-key removal). This
// keeps the per-occurrence drop accounting correct even in the unusual case of a
// profile that references the same key more than once.
func (l profileAttrLeaf) remove(key string) {
	removed := false
	l.indices.RemoveIf(func(idx int32) bool {
		if removed {
			return false
		}
		if l.keyAt(idx) == key {
			removed = true
			return true
		}
		return false
	})
}

// processProfiles enforces the attribute limit on every profile, counted as
// resource + scope + profile attributes (the profile-level cap). Resource and
// scope are plain maps; the profile's own attributes are the tier-10 leaf.
func (p *attributeLimitProcessor) processProfiles(_ context.Context, pd pprofile.Profiles) (pprofile.Profiles, error) {
	dic := pd.Dictionary()
	table := dic.AttributeTable()
	strs := dic.StringTable()

	rps := pd.ResourceProfiles()
	for i := 0; i < rps.Len(); i++ {
		rp := rps.At(i)
		resourceAttrs := rp.Resource().Attributes()

		// Unconditional removal (always runs).
		p.removeUnconditionalAttributes(resourceAttrs)

		// Resource attributes are shared across all profiles under this
		// ResourceProfiles. If an earlier profile is over the limit and drops
		// resource attributes, later profiles see the already-trimmed set. This
		// is by design -- the shared resource attributes need trimming regardless.
		sps := rp.ScopeProfiles()
		for j := 0; j < sps.Len(); j++ {
			sp := sps.At(j)
			scopeAttrs := sp.Scope().Attributes()

			profs := sp.Profiles()
			for k := 0; k < profs.Len(); k++ {
				prof := profs.At(k)
				leaf := profileAttrLeaf{indices: prof.AttributeIndices(), table: table, strings: strs}
				enforceLimit(p, resourceAttrs, scopeAttrs, leaf, "profile", profileSubject(prof, strs))
			}
		}
	}
	return pd, nil
}

// profileSubject returns a human-readable label for rate-limited logging,
// derived from the profile's sample type. Falls back to "profile" when the sample type is absent or unresolvable.
func profileSubject(prof pprofile.Profile, strs pcommon.StringSlice) string {
	idx := int(prof.SampleType().TypeStrindex())
	if idx >= 0 && idx < strs.Len() {
		if s := strs.At(idx); s != "" {
			return s
		}
	}
	return "profile"
}
