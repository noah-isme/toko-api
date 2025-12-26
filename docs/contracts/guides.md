# Frontend Implementation Guides

## Authentication Flow

1. **Login/Register** → Simpan `accessToken` di memory (state management)
2. **Store** `refresh_token` automatically via HTTP-only cookie
3. **Auto-refresh** token sebelum expired (set interval 14 menit)
4. **Logout** → Clear token dari memory dan call logout endpoint

## Cart Management

1. **Guest User:**
   - Generate `anonId` di localStorage saat pertama kali
   - Create cart dengan `anonId`
   - Simpan `cartId` di localStorage
   
2. **Setelah Login:**
   - Call `/api/v1/carts/merge` dengan `cartId` dari localStorage
   - Replace `cartId` dengan user cart
   - Hapus `anonId` dari localStorage

## Error Handling

```typescript
try {
  const response = await api.post('/api/v1/carts/123/items', payload);
  // handle success
} catch (error) {
  if (error.response?.status === 401) {
    // Token expired, try refresh
    await refreshToken();
    // Retry request
  } else if (error.response?.data?.error?.code === 'OUT_OF_STOCK') {
    // Show out of stock message
    showError('Product is out of stock');
  } else {
    // Generic error
    showError(error.response?.data?.error?.message || 'Something went wrong');
  }
}
```

## Optimistic Updates

Untuk update quantity cart:
```typescript
// 1. Update UI immediately
updateCartItemQtyInState(itemId, newQty);

// 2. Call API
try {
  await api.patch(`/api/v1/carts/${cartId}/items/${itemId}`, { qty: newQty });
} catch (error) {
  // 3. Rollback on error
  revertCartItemQty(itemId);
  showError(error);
}
```

## Pagination

```typescript
const fetchProducts = async (page = 1, limit = 20) => {
  const response = await api.get('/api/v1/products', {
    params: { page, limit, category: 'electronics' }
  });
  
  return {
    products: response.data.data,
    totalPages: Math.ceil(response.data.pagination.totalItems / limit),
    currentPage: response.data.pagination.page
  };
};
```
