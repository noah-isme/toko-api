# Returns & Support

Customer endpoints require authentication and tenant membership:

- `POST /api/v1/orders/{orderId}/returns`
- `GET /api/v1/returns` and `GET /api/v1/returns/{returnId}`
- `GET/POST /api/v1/support/tickets`
- `POST /api/v1/support/tickets/{ticketId}/messages`

Admin workflows are available under `/api/v1/admin/returns` and `/api/v1/admin/support/tickets`, including status updates, refunds, and agent messages.
