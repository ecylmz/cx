package cx

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type subscriptionTier struct {
	id, label string
}

var subscriptionTiers = map[provider][]subscriptionTier{
	providerCodex: {
		{"free", "Free"},
		{"go", "Go"},
		{"plus", "Plus"},
		{"pro-100", "Pro 100"},
		{"pro-200", "Pro 200"},
		{"pro-500", "Pro 500"},
		{"business-standard", "Business Standard"},
		{"business-premium", "Business Premium"},
		{"business-codex", "Business Codex"},
		{"enterprise", "Enterprise"},
		{"enterprise-usage", "Enterprise Usage-Based"},
		{"enterprise-automation", "Enterprise Automation"},
		{"edu", "Edu"},
		{"edu-plus", "Edu Plus"},
		{"edu-pro", "Edu Pro"},
		{"teachers", "Teachers"},
		{"healthcare", "Healthcare"},
		{"clinicians", "Clinicians"},
	},
	providerAgy: {
		{"free", "Free"},
		{"plus", "Plus"},
		{"pro", "Pro"},
		{"pro-trial", "Pro Trial"},
		{"ultra-100", "Ultra 100"},
		{"ultra-200", "Ultra 200"},
		{"enterprise-standard", "Enterprise Standard"},
		{"enterprise-plus", "Enterprise Plus"},
		{"enterprise-payg", "Enterprise Pay-as-you-go"},
	},
}

func canonicalTier(p provider, tier string) string {
	if p == providerCodex {
		switch tier {
		case "prolite":
			return "pro-100"
		case "pro":
			return "pro-200"
		case "promax":
			return "pro-500"
		case "team", "business", "business_standard":
			return "business-standard"
		case "self_serve_business_prolite", "business_premium":
			return "business-premium"
		case "self_serve_business_usage_based":
			return "business-codex"
		case "ent26":
			return "enterprise"
		case "enterprise_cbp_usage_based":
			return "enterprise-usage"
		case "enterprise_cbp_automation":
			return "enterprise-automation"
		case "education":
			return "edu"
		case "edu_plus":
			return "edu-plus"
		case "edu_pro":
			return "edu-pro"
		case "k12":
			return "teachers"
		}
	}
	return tier
}

func availableSubscription(p provider, tier string) (subscription, error) {
	sub := newSubscription(p, tier)
	if slices.ContainsFunc(subscriptionTiers[p], func(t subscriptionTier) bool { return t.id == sub.tier() }) {
		return sub, nil
	}
	ids := make([]string, 0, len(subscriptionTiers[p]))
	for _, t := range subscriptionTiers[p] {
		ids = append(ids, t.id)
	}
	return "", fmt.Errorf("unknown %s subscription %q. Choose from %s", p.label(), tier, strings.Join(ids, ", "))
}

func verifySubscription(expected subscription, reported string) error {
	if expected == "" {
		return nil
	}
	reported = strings.ToLower(strings.TrimSpace(reported))
	if reported == "" {
		return nil
	}
	var matching []string
	if expected.provider() == providerAgy {
		reported = strings.NewReplacer("(", "", ")", "").Replace(reported)
		reported = strings.TrimPrefix(reported, "google ai ")
		reported = strings.TrimPrefix(reported, "gemini ")
		switch reported {
		case "pro":
			matching = []string{"pro", "pro-trial"}
		case "pro paid":
			matching = []string{"pro"}
		case "ultra":
			matching = []string{"ultra-100", "ultra-200"}
		case "ultra 5x":
			matching = []string{"ultra-100"}
		case "ultra 20x":
			matching = []string{"ultra-200"}
		case "enterprise":
			matching = []string{"enterprise-standard", "enterprise-plus", "enterprise-payg"}
		}
	} else if reported == "business" {
		matching = []string{"business-standard", "business-premium", "business-codex"}
	}
	if matching == nil {
		for _, tier := range subscriptionTiers[expected.provider()] {
			if canonicalTier(expected.provider(), reported) == tier.id || strings.EqualFold(reported, tier.label) {
				matching = []string{tier.id}
				break
			}
		}
	}
	if len(matching) > 0 && !slices.Contains(matching, expected.tier()) {
		return fmt.Errorf("subscription mismatch: expected %s, but sign-in reported %s", expected.label(), reported)
	}
	return nil
}

func changeSubscription(p paths, a Account, tier string) error {
	sub, err := availableSubscription(a.provider(), tier)
	if err != nil {
		return err
	}
	lock, err := lockState(p)
	if err != nil {
		return err
	}
	defer lock.Close()
	var stored Account
	if err := readJSON(p.accountMeta(a.ID), &stored); err != nil {
		return err
	}
	if stored.ID != a.ID || stored.subscription() != a.subscription() {
		return fmt.Errorf("account changed: %s", a.Name)
	}
	if stored.subscription() == sub {
		return nil
	}
	stored.Subscription, stored.UpdatedAt = sub, time.Now()
	return writeJSON(p.accountMeta(stored.ID), stored)
}
