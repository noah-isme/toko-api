#!/bin/bash
set -e
BASE_URL="http://localhost:8080/api/v1"
EMAIL="verify_$(date +%s)@test.com"
PASSWORD="Password123!"

# Helper function
check() {
    local name="$1"
    local expected="$2"
    local actual="$3"
    if [ "$actual" -eq "$expected" ]; then
        echo "PASS"
    else
        echo "FAIL (expected $expected, got $actual)"
        exit 1
    fi
}

echo "=== COMPLETE API VERIFICATION ==="
echo ""

# =====================
# 1. AUTHENTICATION
# =====================
echo ">>> 1. AUTHENTICATION"
echo -n "  Register... "
REG_RES=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/auth/register \
  -H "Content-Type: application/json" \
  -d "{\"name\": \"Verify User\", \"email\": \"$EMAIL\", \"password\": \"$PASSWORD\"}")
REG_CODE=$(echo "$REG_RES" | tail -n1)
check "Register" 201 $REG_CODE

echo -n "  Login... "
LOGIN_RES=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/auth/login \
  -H "Content-Type: application/json" \
  -d "{\"email\": \"$EMAIL\", \"password\": \"$PASSWORD\"}")
LOGIN_BODY=$(echo "$LOGIN_RES" | head -n1)
LOGIN_CODE=$(echo "$LOGIN_RES" | tail -n1)
check "Login" 200 $LOGIN_CODE
TOKEN=$(echo $LOGIN_BODY | jq -r '.data.accessToken')

echo -n "  Get Me... "
ME_RES=$(curl -s -w "\n%{http_code}" -X GET $BASE_URL/auth/me -H "Authorization: Bearer $TOKEN")
ME_CODE=$(echo "$ME_RES" | tail -n1)
check "Get Me" 200 $ME_CODE

echo -n "  Refresh Token... "
REFRESH_RES=$(curl -s -w "\n%{http_code}" --max-time 5 -X POST $BASE_URL/auth/refresh)
REFRESH_CODE=$(echo "$REFRESH_RES" | tail -n1)
# Refresh requires cookie, so 401 is expected without it. Just verify endpoint exists.
if [ "$REFRESH_CODE" -eq 200 ] || [ "$REFRESH_CODE" -eq 401 ]; then echo "PASS (endpoint exists)"; else echo "FAIL ($REFRESH_CODE)"; exit 1; fi

# =====================
# 2. CATALOG
# =====================
echo ""
echo ">>> 2. CATALOG"
echo -n "  List Categories... "
CAT_RES=$(curl -s -w "\n%{http_code}" "$BASE_URL/categories")
CAT_CODE=$(echo "$CAT_RES" | tail -n1)
check "Categories" 200 $CAT_CODE

echo -n "  List Brands... "
BRAND_RES=$(curl -s -w "\n%{http_code}" "$BASE_URL/brands")
BRAND_CODE=$(echo "$BRAND_RES" | tail -n1)
check "Brands" 200 $BRAND_CODE

echo -n "  List Products... "
PROD_RES=$(curl -s -w "\n%{http_code}" "$BASE_URL/products?limit=5")
PROD_BODY=$(echo "$PROD_RES" | head -n1)
PROD_CODE=$(echo "$PROD_RES" | tail -n1)
check "Products List" 200 $PROD_CODE

SLUG=$(echo $PROD_BODY | jq -r '.data[0].slug')
PROD_ID=$(echo $PROD_BODY | jq -r '.data[0].id')

echo -n "  Product Detail ($SLUG)... "
DETAIL_RES=$(curl -s -w "\n%{http_code}" "$BASE_URL/products/$SLUG")
DETAIL_BODY=$(echo "$DETAIL_RES" | head -n1)
DETAIL_CODE=$(echo "$DETAIL_RES" | tail -n1)
check "Product Detail" 200 $DETAIL_CODE

VARIANT_ID=$(echo $DETAIL_BODY | jq -r '.data.variants[0].id // empty')

echo -n "  Related Products... "
RELATED_RES=$(curl -s -w "\n%{http_code}" "$BASE_URL/products/$SLUG/related")
RELATED_CODE=$(echo "$RELATED_RES" | tail -n1)
check "Related Products" 200 $RELATED_CODE

# =====================
# 3. ADDRESSES
# =====================
echo ""
echo ">>> 3. ADDRESSES"
echo -n "  Create Address... "
ADDR_PAYLOAD='{"label":"Home","receiver_name":"Test User","phone":"08123456789","address_line1":"Jl. Test No. 1","city":"Jakarta","postal_code":"12345","country":"Indonesia","is_default":true}'
CREATE_ADDR_RES=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/users/me/addresses \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "$ADDR_PAYLOAD")
CREATE_ADDR_BODY=$(echo "$CREATE_ADDR_RES" | head -n1)
CREATE_ADDR_CODE=$(echo "$CREATE_ADDR_RES" | tail -n1)
check "Create Address" 201 $CREATE_ADDR_CODE
ADDR_ID=$(echo $CREATE_ADDR_BODY | jq -r '.data.id')

echo -n "  List Addresses... "
LIST_ADDR_RES=$(curl -s -w "\n%{http_code}" -X GET $BASE_URL/users/me/addresses -H "Authorization: Bearer $TOKEN")
LIST_ADDR_CODE=$(echo "$LIST_ADDR_RES" | tail -n1)
check "List Addresses" 200 $LIST_ADDR_CODE

echo -n "  Update Address... "
UPDATE_ADDR_RES=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE_URL/users/me/addresses/$ADDR_ID" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{"label":"Office"}')
UPDATE_ADDR_CODE=$(echo "$UPDATE_ADDR_RES" | tail -n1)
check "Update Address" 200 $UPDATE_ADDR_CODE

# =====================
# 4. CART & VOUCHER
# =====================
echo ""
echo ">>> 4. CART & VOUCHER"
echo -n "  Create Cart... "
CART_RES=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/carts -H "Content-Type: application/json" -d '{}')
CART_BODY=$(echo "$CART_RES" | head -n1)
CART_CODE=$(echo "$CART_RES" | tail -n1)
check "Create Cart" 201 $CART_CODE
CART_ID=$(echo $CART_BODY | jq -r '.data.cartId')

echo -n "  Add Item... "
if [ -z "$VARIANT_ID" ]; then PAYLOAD="{\"productId\": \"$PROD_ID\", \"qty\": 2}"; else PAYLOAD="{\"productId\": \"$PROD_ID\", \"variantId\": \"$VARIANT_ID\", \"qty\": 2}"; fi
ADD_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/carts/$CART_ID/items" -H "Content-Type: application/json" -d "$PAYLOAD")
ADD_CODE=$(echo "$ADD_RES" | tail -n1)
check "Add Item" 200 $ADD_CODE

echo -n "  Get Cart... "
GET_CART_RES=$(curl -s -w "\n%{http_code}" "$BASE_URL/carts/$CART_ID")
GET_CART_CODE=$(echo "$GET_CART_RES" | tail -n1)
check "Get Cart" 200 $GET_CART_CODE

ITEM_ID=$(echo "$GET_CART_RES" | head -n1 | jq -r '.data.items[0].id')

echo -n "  Update Item Qty... "
UPDATE_QTY_RES=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE_URL/carts/$CART_ID/items/$ITEM_ID" \
  -H "Content-Type: application/json" -d '{"qty": 1}')
UPDATE_QTY_CODE=$(echo "$UPDATE_QTY_RES" | tail -n1)
check "Update Qty" 200 $UPDATE_QTY_CODE

echo -n "  Apply Voucher (DISC20)... "
V_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/carts/$CART_ID/apply-voucher" \
  -H "Content-Type: application/json" -d '{"code":"DISC20"}')
V_CODE=$(echo "$V_RES" | tail -n1)
check "Apply Voucher" 200 $V_CODE

echo -n "  Remove Voucher... "
RV_RES=$(curl -s -w "\n%{http_code}" -X DELETE "$BASE_URL/carts/$CART_ID/voucher")
RV_CODE=$(echo "$RV_RES" | tail -n1)
check "Remove Voucher" 200 $RV_CODE

echo -n "  Shipping Quote... "
SHIP_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/carts/$CART_ID/quote/shipping" \
  -H "Content-Type: application/json" -d '{"destination":"Jakarta Selatan", "courier":"jne", "weightGram": 1000}')
SHIP_CODE=$(echo "$SHIP_RES" | tail -n1)
check "Shipping Quote" 200 $SHIP_CODE

echo -n "  Tax Quote... "
TAX_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/carts/$CART_ID/quote/tax")
TAX_CODE=$(echo "$TAX_RES" | tail -n1)
check "Tax Quote" 200 $TAX_CODE

# =====================
# 5. CHECKOUT & ORDERS
# =====================
echo ""
echo ">>> 5. CHECKOUT & ORDERS"

# Re-apply voucher for checkout
curl -s -X POST "$BASE_URL/carts/$CART_ID/apply-voucher" -H "Content-Type: application/json" -d '{"code":"DISC20"}' > /dev/null

echo -n "  Create Order (Checkout)... "
ORDER_PAYLOAD="{\"cartId\": \"$CART_ID\", \"address\": {\"receiverName\":\"Test\",\"phone\":\"08123456789\",\"addressLine1\":\"Jl. Test\",\"city\":\"Jakarta\",\"postalCode\":\"12345\",\"country\":\"Indonesia\"}, \"shipping\": {\"courier\":\"jne\",\"service\":\"REG\",\"price\":15000,\"etd\":\"2-3 days\"}}"
ORDER_RES=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/checkout \
  -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" -d "$ORDER_PAYLOAD")
ORDER_BODY=$(echo "$ORDER_RES" | head -n1)
ORDER_CODE=$(echo "$ORDER_RES" | tail -n1)
check "Create Order" 201 $ORDER_CODE
ORDER_ID=$(echo $ORDER_BODY | jq -r '.data.orderId')

echo -n "  List Orders... "
LIST_ORDERS_RES=$(curl -s -w "\n%{http_code}" -X GET $BASE_URL/orders -H "Authorization: Bearer $TOKEN")
LIST_ORDERS_CODE=$(echo "$LIST_ORDERS_RES" | tail -n1)
check "List Orders" 200 $LIST_ORDERS_CODE

echo -n "  Get Order Detail... "
GET_ORDER_RES=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/orders/$ORDER_ID" -H "Authorization: Bearer $TOKEN")
GET_ORDER_CODE=$(echo "$GET_ORDER_RES" | tail -n1)
check "Get Order" 200 $GET_ORDER_CODE

# =====================
# 6. HEALTH
# =====================
echo ""
echo ">>> 6. HEALTH"
echo -n "  Liveness... "
LIVE_RES=$(curl -s -w "\n%{http_code}" "http://localhost:8080/health/live")
LIVE_CODE=$(echo "$LIVE_RES" | tail -n1)
check "Liveness" 200 $LIVE_CODE

echo -n "  Readiness... "
READY_RES=$(curl -s -w "\n%{http_code}" "http://localhost:8080/health/ready")
READY_CODE=$(echo "$READY_RES" | tail -n1)
check "Readiness" 200 $READY_CODE

# =====================
# CLEANUP - Delete Address (optional)
# =====================
echo ""
echo ">>> 7. CLEANUP"
echo -n "  Delete Address... "
DEL_ADDR_RES=$(curl -s -w "\n%{http_code}" -X DELETE "$BASE_URL/users/me/addresses/$ADDR_ID" -H "Authorization: Bearer $TOKEN")
DEL_ADDR_CODE=$(echo "$DEL_ADDR_RES" | tail -n1)
# 200 or 204 both acceptable
if [ "$DEL_ADDR_CODE" -eq 200 ] || [ "$DEL_ADDR_CODE" -eq 204 ]; then echo "PASS"; else echo "FAIL ($DEL_ADDR_CODE)"; exit 1; fi

echo ""
echo "=========================================="
echo ">>> ALL ENDPOINT TESTS PASSED! <<<"
echo "=========================================="
