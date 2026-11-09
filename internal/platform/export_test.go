package platform

import "time"

// SetRefundTimeoutsForTest shortens the refund gateway/write budgets and returns a
// restore func.
func SetRefundTimeoutsForTest(gateway, write time.Duration) func() {
	prevGateway, prevWrite := refundGatewayTimeout, refundWriteTimeout
	refundGatewayTimeout, refundWriteTimeout = gateway, write
	return func() { refundGatewayTimeout, refundWriteTimeout = prevGateway, prevWrite }
}
