package shipping

import (
	"strings"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// MapExternalToStatus converts external provider status labels into internal shipment statuses.
func MapExternalToStatus(external string) dbgen.ShipmentStatus {
	switch strings.ToLower(strings.TrimSpace(external)) {
	case "picked", "pickup":
		return dbgen.ShipmentStatusPICKED
	case "shipped", "in_transit", "in-transit":
		return dbgen.ShipmentStatusSHIPPED
	case "out_for_delivery", "out-for-delivery":
		return dbgen.ShipmentStatusOUTFORDELIVERY
	case "delivered":
		return dbgen.ShipmentStatusDELIVERED
	}
	return dbgen.ShipmentStatusPENDING
}
