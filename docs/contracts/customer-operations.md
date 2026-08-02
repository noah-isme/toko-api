# Returns & Support

Customer endpoints require authentication and tenant membership:

- `POST /api/v1/orders/{orderId}/returns`
- `GET /api/v1/returns` and `GET /api/v1/returns/{returnId}`
- `GET/POST /api/v1/support/tickets`
- `GET /api/v1/support/tickets/{ticketId}/messages`
- `POST /api/v1/support/tickets/{ticketId}/messages`

Admin workflows are available under `/api/v1/admin/returns` and `/api/v1/admin/support/tickets`, including status updates, refunds, message history, and agent messages. Both customer and admin ticket message endpoints return chronological transcripts.
