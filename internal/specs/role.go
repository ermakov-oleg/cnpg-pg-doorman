package specs

import "fmt"

// GetRBACName returns the RBAC entity name for the pg-doorman plugin.
func GetRBACName(clusterName string) string {
	return fmt.Sprintf("%s-pg-doorman", clusterName)
}
