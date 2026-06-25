package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/store"
)

const (
	suppressionSourceHygiene = "hygiene"
	suppressionSourceReview  = "review"
	suppressionSourceScan    = "scan"
)

type suppressionTarget struct {
	Source      string
	Code        string
	Environment string
	Key         string
	Path        string
	Line        int
	Column      int
	Kind        string
	Fingerprint string

	AlternateFingerprints []string
}

func activeSuppressionRecords(repo store.Repository) ([]domain.SuppressionRecord, error) {
	entries, err := repo.Suppressions(time.Now().UTC())
	if err != nil {
		return nil, err
	}
	records := []domain.SuppressionRecord{}
	for _, entry := range entries {
		if entry.ValidSignature && !entry.Expired {
			records = append(records, entry.Suppression)
		}
	}
	return records, nil
}

func matchingSuppressionTarget(target suppressionTarget, suppressions []domain.SuppressionRecord) (domain.SuppressionRecord, bool) {
	for _, suppression := range suppressions {
		if !suppressionMatchesSource(suppression, target.Source) {
			continue
		}
		if suppression.Code != target.Code {
			continue
		}
		if suppression.Fingerprint != "" {
			if !suppressionMatchesTargetFingerprint(suppression.Fingerprint, target) {
				continue
			}
			return suppression, true
		}
		if suppression.Environment != "" && suppression.Environment != target.Environment {
			continue
		}
		if suppression.Key != "" && suppression.Key != target.Key {
			continue
		}
		if suppression.Path != "" && normalizeSuppressionPathForMatch(suppression.Path) != normalizeSuppressionPathForMatch(target.Path) {
			continue
		}
		if suppression.Line > 0 && suppression.Line != target.Line && suppressionRequiresExactLocation(suppression) {
			continue
		}
		if suppression.Column > 0 && suppression.Column != target.Column && suppressionRequiresExactLocation(suppression) {
			continue
		}
		if suppression.Kind != "" && suppression.Kind != target.Kind {
			continue
		}
		return suppression, true
	}
	return domain.SuppressionRecord{}, false
}

func suppressionMatchesTargetFingerprint(fingerprint string, target suppressionTarget) bool {
	if fingerprint == target.Fingerprint {
		return true
	}
	for _, alternate := range target.AlternateFingerprints {
		if fingerprint == alternate {
			return true
		}
	}
	return false
}

func suppressionRequiresExactLocation(suppression domain.SuppressionRecord) bool {
	return !suppressionMatchesSource(suppression, suppressionSourceReview) && !suppressionMatchesSource(suppression, suppressionSourceScan)
}

func suppressionMatchesSource(suppression domain.SuppressionRecord, source string) bool {
	source = strings.TrimSpace(source)
	recordSource := strings.TrimSpace(suppression.Source)
	if recordSource == "" {
		return source == suppressionSourceHygiene
	}
	return recordSource == source
}

func normalizeSuppressionPathForMatch(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.TrimPrefix(value, "./")
	return value
}

func suppressionFingerprint(parts ...string) string {
	normalized := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			part = "-"
		}
		normalized = append(normalized, part)
	}
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func reviewSuppressionFingerprint(code string, env string, key string, path string) string {
	return suppressionFingerprint(
		suppressionSourceReview,
		code,
		env,
		key,
		normalizeSuppressionPathForMatch(path),
	)
}

func scanSuppressionFingerprint(path string, key string, kind string, evidenceDigest string, occurrence int) string {
	return suppressionFingerprint(
		suppressionSourceScan,
		scanSuppressionCode,
		normalizeSuppressionPathForMatch(path),
		key,
		kind,
		evidenceDigest,
		fmt.Sprintf("%d", occurrence),
	)
}

func legacyScanSuppressionFingerprint(path string, key string, kind string) string {
	return suppressionFingerprint(
		suppressionSourceScan,
		scanSuppressionCode,
		normalizeSuppressionPathForMatch(path),
		key,
		kind,
	)
}

func suppressionPromptLabel(parts ...string) string {
	return truncatePromptText(joinPromptParts(parts...), 30)
}

func suppressionPromptDescription(parts ...string) string {
	return truncatePromptText(joinPromptParts(parts...), 34)
}

func joinPromptParts(parts ...string) string {
	kept := []string{}
	for _, part := range parts {
		part = strings.Join(strings.Fields(part), " ")
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " ")
}

func truncatePromptText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}

func suppressionPromptLocation(path string, line int, column int) string {
	location := compactPromptPath(path, 28)
	if location == "" {
		return ""
	}
	if line > 0 && column > 0 {
		return fmt.Sprintf("%s:%d:%d", location, line, column)
	}
	if line > 0 {
		return fmt.Sprintf("%s:%d", location, line)
	}
	return location
}

func compactPromptPath(path string, limit int) string {
	path = normalizeSuppressionPathForMatch(path)
	if limit <= 0 || len(path) <= limit {
		return path
	}
	base := path
	if index := strings.LastIndex(path, "/"); index >= 0 && index+1 < len(path) {
		base = path[index+1:]
	}
	if len(base)+4 <= limit {
		return ".../" + base
	}
	if limit <= 3 {
		return path[len(path)-limit:]
	}
	return "..." + path[len(path)-(limit-3):]
}
