package views

import "github.com/mark3labs/msbd/internal/dashboard/components/badge"

// badgeVariant maps a sandbox state string to a templui badge variant.
func badgeVariant(state string) badge.Variant {
	switch stateBadge(state) {
	case "default":
		return badge.VariantDefault
	case "secondary":
		return badge.VariantSecondary
	case "destructive":
		return badge.VariantDestructive
	default:
		return badge.VariantOutline
	}
}
