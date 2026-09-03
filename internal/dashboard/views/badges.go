package views

import "github.com/mark3labs/msbd/internal/dashboard/components/badge"

// badgeVariant maps a sandbox state string to a templui badge variant.
func badgeVariant(state string) badge.Variant {
	return toBadgeVariant(stateBadge(state))
}

// roleVariant maps a user role to a templui badge variant.
func roleVariant(role string) badge.Variant {
	return toBadgeVariant(roleBadge(role))
}

// keyStatusVariant maps an API key status to a templui badge variant.
func keyStatusVariant(status string) badge.Variant {
	return toBadgeVariant(keyStatusBadge(status))
}

func toBadgeVariant(name string) badge.Variant {
	switch name {
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
