package hue

import (
	"errors"
	"fmt"
	"strings"
)

// Users refer to lights and rooms by name ("Kitchen"), not by UUID. Match
// resolves a query against a list, in this order:
//
//  1. exact id
//  2. exact name, case-insensitive
//  3. unique id prefix (at least 4 characters, so "1" does not match)
//  4. unique name prefix, case-insensitive
//
// Ambiguity is an error that lists the candidates rather than a guess.

// ErrNoMatch is returned when nothing matches; errors.Is-compatible.
var ErrNoMatch = errors.New("no match")

// Match is generic over the item type T. The caller passes two small
// functions that extract the id and the name, so the same logic serves
// Light, Group, and whatever comes next.
func Match[T any](items []T, query string, id, name func(T) string) (T, error) {
	var zero T
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return zero, errors.New("empty name")
	}

	for _, it := range items {
		if strings.ToLower(id(it)) == q {
			return it, nil
		}
	}
	var exact []T
	for _, it := range items {
		if strings.ToLower(name(it)) == q {
			exact = append(exact, it)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return zero, ambiguous(query, exact, id, name)
	}

	var prefix []T
	if len(q) >= 4 {
		for _, it := range items {
			if strings.HasPrefix(strings.ToLower(id(it)), q) {
				prefix = append(prefix, it)
			}
		}
	}
	if len(prefix) == 0 {
		for _, it := range items {
			if strings.HasPrefix(strings.ToLower(name(it)), q) {
				prefix = append(prefix, it)
			}
		}
	}
	switch len(prefix) {
	case 1:
		return prefix[0], nil
	case 0:
		return zero, fmt.Errorf("%w for %q", ErrNoMatch, query)
	default:
		return zero, ambiguous(query, prefix, id, name)
	}
}

func ambiguous[T any](query string, candidates []T, id, name func(T) string) error {
	var list []string
	for _, c := range candidates {
		list = append(list, fmt.Sprintf("%s (%s)", name(c), id(c)))
	}
	return fmt.Errorf("%q is ambiguous, matches: %s", query, strings.Join(list, ", "))
}

// MatchLight and MatchGroup are the two concrete uses.
func MatchLight(lights []Light, query string) (Light, error) {
	return Match(lights, query, func(l Light) string { return l.ID }, Light.Name)
}

func MatchGroup(groups []Group, query string) (Group, error) {
	return Match(groups, query, func(g Group) string { return g.ID }, func(g Group) string { return g.Name })
}
