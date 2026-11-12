package entitlements

// Plan ids of the launch tiers (docs/PLAN.md §11.1). Values are data: the
// admin edits them in the database (#36); these are the seed.
const (
	PlanFree = "free"
	PlanPlus = "plus"
	PlanPro  = "pro"
)

// Plans returns the seed tiers.
func Plans() []Plan {
	return []Plan{
		{ID: PlanFree, Name: "Free", Entitlements: Set{
			HivesMax: I(1), MembersMax: I(2), SharesMax: I(2), SessionsConcurrent: I(2), RemoteQuality: S("sub"),
			ExportRemotePerDay: I(5), ExportMaxMinutes: I(5), ExportAPI: B(false), NotifyChannels: S("basic"), NotifyDevices: I(1),
			AnalyticsLevel: S("motion"), PTZEnabled: B(true), RetentionDaysMax: I(Unlimited), UpdatesEarlyAccess: B(false), RemoteAccessEnabled: B(true),
		}},
		{ID: PlanPlus, Name: "Plus", Entitlements: Set{
			HivesMax: I(3), MembersMax: I(4), SharesMax: I(5), SessionsConcurrent: I(4), RemoteQuality: S("full"),
			ExportRemotePerDay: I(Unlimited), ExportMaxMinutes: I(60), ExportAPI: B(false), NotifyChannels: S("rich"), NotifyDevices: I(5),
			AnalyticsLevel: S("objects"), PTZEnabled: B(true), RetentionDaysMax: I(Unlimited), UpdatesEarlyAccess: B(false), RemoteAccessEnabled: B(true),
		}},
		{ID: PlanPro, Name: "Pro", Entitlements: Set{
			HivesMax: I(10), MembersMax: I(14), SharesMax: I(15), SessionsConcurrent: I(10), RemoteQuality: S("full"),
			ExportRemotePerDay: I(Unlimited), ExportMaxMinutes: I(Unlimited), ExportAPI: B(true), NotifyChannels: S("all"), NotifyDevices: I(Unlimited),
			AnalyticsLevel: S("full"), PTZEnabled: B(true), RetentionDaysMax: I(Unlimited), UpdatesEarlyAccess: B(true), RemoteAccessEnabled: B(true),
		}},
	}
}

// PlanByID returns a seed plan.
func PlanByID(id string) (Plan, bool) {
	for _, p := range Plans() {
		if p.ID == id {
			return p, true
		}
	}
	return Plan{}, false
}

// AddOns returns the seed add-ons.
func AddOns() []AddOn {
	return []AddOn{
		{ID: "hive.extra", Name: "Extra hive", Entitlements: Set{HivesMax: I(1)}}, // Pro: +2 €/month each
	}
}
