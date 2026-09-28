package graphfake

// PIMRules are a role management policy's rules: 8 hours, MFA and a justification, an
// approval by one group, and permanent eligible assignments allowed.
func PIMRules() []any {
	return []any{
		map[string]any{"id": "Expiration_EndUser_Assignment", "maximumDuration": "PT8H"},
		map[string]any{"id": "Enablement_EndUser_Assignment", "enabledRules": []any{"MultiFactorAuthentication", "Justification"}},
		map[string]any{"id": "Approval_EndUser_Assignment", "setting": map[string]any{"isApprovalRequired": true, "approvalStages": []any{
			map[string]any{"primaryApprovers": []any{map[string]any{"description": "Security Approvers", "groupId": "g"}, map[string]any{"userId": "u1"}}}}}},
		map[string]any{"id": "AuthenticationContext_EndUser_Assignment", "isEnabled": true, "claimValue": "c1"},
		map[string]any{"id": "Expiration_Admin_Eligibility", "isExpirationRequired": false},
		map[string]any{"id": "Expiration_Admin_Assignment", "isExpirationRequired": true, "maximumDuration": "P180D"},
	}
}
