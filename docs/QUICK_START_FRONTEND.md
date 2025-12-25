# Quick Start Guide untuk Frontend Developer

Panduan cepat integrasi API Toko untuk tim frontend.

## 📦 Setup Awal

### Base URL
```javascript
// .env.local
NEXT_PUBLIC_API_BASE_URL=http://localhost:8080
# atau production
NEXT_PUBLIC_API_BASE_URL=https://api.toko.com
```

### API Client Setup (Axios)

```typescript
// lib/api.ts
import axios from 'axios';

const api = axios.create({
  baseURL: process.env.NEXT_PUBLIC_API_BASE_URL,
  timeout: 30000,
  withCredentials: true, // Important! Untuk cookie refresh_token
});

// Request interceptor - tambah token
api.interceptors.request.use((config) => {
  const token = localStorage.getItem('accessToken');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

// Response interceptor - handle refresh token
api.interceptors.response.use(
  (response) => response,
  async (error) => {
    const originalRequest = error.config;
    
    // Jika token expired dan belum retry
    if (error.response?.status === 401 && !originalRequest._retry) {
      originalRequest._retry = true;
      
      try {
        // Refresh token (cookie otomatis dikirim)
        const { data } = await axios.post(
          `${process.env.NEXT_PUBLIC_API_BASE_URL}/api/v1/auth/refresh`,
          {},
          { withCredentials: true }
        );
        
        // Simpan token baru
        localStorage.setItem('accessToken', data.data.accessToken);
        
        // Retry request dengan token baru
        originalRequest.headers.Authorization = `Bearer ${data.data.accessToken}`;
        return api(originalRequest);
      } catch (refreshError) {
        // Refresh gagal, redirect ke login
        localStorage.removeItem('accessToken');
        window.location.href = '/login';
        return Promise.reject(refreshError);
      }
    }
    
    return Promise.reject(error);
  }
);

export default api;
```

---

## 🔐 Authentication Flow

### 1. Register
```typescript
// hooks/useAuth.ts
export const useAuth = () => {
  const register = async (name: string, email: string, password: string) => {
    try {
      const { data } = await api.post('/api/v1/auth/register', {
        name,
        email,
        password,
      });
      
      // Simpan token
      localStorage.setItem('accessToken', data.data.accessToken);
      
      // Refresh token otomatis tersimpan di cookie
      return data.data.user;
    } catch (error) {
      throw error.response?.data?.error;
    }
  };
  
  return { register };
};
```

### 2. Login
```typescript
const login = async (email: string, password: string) => {
  const { data } = await api.post('/api/v1/auth/login', {
    email,
    password,
  });
  
  localStorage.setItem('accessToken', data.data.accessToken);
  return data.data.user;
};
```

### 3. Auto-refresh Token
```typescript
// Setup timer untuk auto-refresh sebelum expired
useEffect(() => {
  // Refresh setiap 14 menit (token expired 15 menit)
  const interval = setInterval(async () => {
    try {
      const { data } = await api.post('/api/v1/auth/refresh');
      localStorage.setItem('accessToken', data.data.accessToken);
    } catch (error) {
      console.error('Failed to refresh token');
    }
  }, 14 * 60 * 1000); // 14 menit
  
  return () => clearInterval(interval);
}, []);
```

### 4. Logout
```typescript
const logout = async () => {
  await api.post('/api/v1/auth/logout');
  localStorage.removeItem('accessToken');
  router.push('/login');
};
```

---

## 🛒 Cart Management (Guest & User)

### Setup Cart State
```typescript
// hooks/useCart.ts
import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface CartStore {
  cartId: string | null;
  anonId: string | null;
  setCartId: (id: string) => void;
  setAnonId: (id: string) => void;
  clearCart: () => void;
}

export const useCartStore = create<CartStore>()(
  persist(
    (set) => ({
      cartId: null,
      anonId: null,
      setCartId: (id) => set({ cartId: id }),
      setAnonId: (id) => set({ anonId: id }),
      clearCart: () => set({ cartId: null, anonId: null }),
    }),
    {
      name: 'cart-storage',
    }
  )
);
```

### Flow untuk Guest User
```typescript
// 1. Pertama kali buka aplikasi
const initGuestCart = async () => {
  const { cartId, anonId } = useCartStore.getState();
  
  // Jika belum punya cart, buat baru
  if (!cartId) {
    const { data } = await api.post('/api/v1/carts', {});
    useCartStore.setState({
      cartId: data.data.cartId,
      anonId: data.data.anonId,
    });
  }
};

// 2. Add item ke cart
const addToCart = async (productId: string, qty: number) => {
  const { cartId } = useCartStore.getState();
  
  // Pastikan ada cart dulu
  if (!cartId) await initGuestCart();
  
  const { data } = await api.post(`/api/v1/carts/${cartId}/items`, {
    productId,
    qty,
  });
  
  return data.data; // Updated cart
};

// 3. Get cart
const getCart = async () => {
  const { cartId } = useCartStore.getState();
  if (!cartId) return null;
  
  const { data } = await api.get(`/api/v1/carts/${cartId}`);
  return data.data;
};
```

### Merge Cart setelah Login
```typescript
// Panggil setelah login sukses
const mergeGuestCart = async () => {
  const { cartId, anonId } = useCartStore.getState();
  
  // Jika punya guest cart, merge ke user cart
  if (cartId && anonId) {
    try {
      const { data } = await api.post('/api/v1/carts/merge', {
        cartId,
      });
      
      // Update dengan user cart ID
      useCartStore.setState({
        cartId: data.data.cartId,
        anonId: null, // Clear anonId
      });
    } catch (error) {
      console.error('Failed to merge cart:', error);
    }
  }
};

// Usage
const handleLogin = async (email, password) => {
  await login(email, password);
  await mergeGuestCart(); // Merge setelah login
  router.push('/');
};
```

---

## 📦 Product Catalog

### List Products dengan Filter
```typescript
interface ProductFilters {
  q?: string;
  category?: string;
  brand?: string;
  minPrice?: number;
  maxPrice?: number;
  inStock?: boolean;
  sort?: 'price:asc' | 'price:desc' | 'title:asc' | 'title:desc';
  page?: number;
  limit?: number;
}

const fetchProducts = async (filters: ProductFilters) => {
  const { data } = await api.get('/api/v1/products', { params: filters });
  return {
    products: data.data,
    pagination: data.pagination,
  };
};

// Example usage
const { products, pagination } = await fetchProducts({
  category: 'smartphones',
  minPrice: 5000000,
  maxPrice: 15000000,
  sort: 'price:asc',
  page: 1,
  limit: 20,
});
```

### Search Products
```typescript
const searchProducts = async (query: string) => {
  const { data } = await api.get('/api/v1/products', {
    params: { q: query, limit: 10 },
  });
  return data.data;
};

// Dengan debounce
import { debounce } from 'lodash';

const debouncedSearch = debounce(async (query: string) => {
  if (query.length < 3) return;
  const results = await searchProducts(query);
  setSearchResults(results);
}, 300);
```

---

## 🛍️ Checkout Flow

### Step 1: Validate Cart
```typescript
const cart = await getCart();
if (!cart.items.length) {
  return showError('Cart is empty');
}
```

### Step 2: Select Shipping Address
```typescript
// Fetch user addresses
const { data } = await api.get('/api/v1/users/me/addresses');
const addresses = data.data;

// Or create new address
const newAddress = await api.post('/api/v1/users/me/addresses', {
  label: 'Home',
  receiver_name: 'John Doe',
  phone: '+6281234567890',
  country: 'Indonesia',
  province: 'DKI Jakarta',
  city: 'Jakarta Selatan',
  postal_code: '12190',
  address_line1: 'Jl. Sudirman No. 123',
  address_line2: '',
  is_default: true,
});
```

### Step 3: Get Shipping Quote
```typescript
const getShippingRates = async (destination: string) => {
  const { cartId } = useCartStore.getState();
  const { data } = await api.post(`/api/v1/carts/${cartId}/quote/shipping`, {
    destination,
    courier: 'jne',
    weightGram: 1000, // Calculate from cart items
  });
  
  return data.data; // Array of shipping services
};
```

### Step 4: Create Order
```typescript
const checkout = async (checkoutData: {
  shippingAddressId: string;
  shippingService: string;
  shippingCost: number;
  paymentMethod: string;
  notes?: string;
}) => {
  const { cartId } = useCartStore.getState();
  
  const { data } = await api.post('/api/v1/checkout', {
    cartId,
    ...checkoutData,
  });
  
  // Clear cart after checkout
  useCartStore.setState({ cartId: null });
  
  // Redirect ke payment
  window.location.href = data.data.paymentUrl;
  
  return data.data;
};
```

---

## 📱 Optimistic Updates

### Update Cart Item Quantity
```typescript
const updateCartItemQty = async (itemId: string, qty: number) => {
  const { cartId } = useCartStore.getState();
  
  // 1. Optimistic update UI
  setCartItems((prev) =>
    prev.map((item) => (item.id === itemId ? { ...item, qty } : item))
  );
  
  try {
    // 2. API call
    const { data } = await api.patch(
      `/api/v1/carts/${cartId}/items/${itemId}`,
      { qty }
    );
    
    // 3. Update dengan response dari server (recalculated pricing)
    setCart(data.data);
  } catch (error) {
    // 4. Rollback on error
    await getCart(); // Re-fetch cart
    showError(error.response?.data?.error?.message);
  }
};
```

---

## 🎫 Voucher Management

### Apply Voucher
```typescript
const applyVoucher = async (code: string) => {
  const { cartId } = useCartStore.getState();
  
  try {
    const { data } = await api.post(
      `/api/v1/carts/${cartId}/apply-voucher`,
      { code: code.toUpperCase() }
    );
    
    showSuccess(`Voucher applied! Discount: ${formatCurrency(data.data.discount)}`);
    
    // Refresh cart untuk update pricing
    await getCart();
  } catch (error) {
    const errorCode = error.response?.data?.error?.code;
    
    switch (errorCode) {
      case 'VOUCHER_INVALID':
        showError('Voucher tidak valid atau sudah expired');
        break;
      case 'VOUCHER_MIN_SPEND':
        showError('Belum memenuhi minimum pembelian');
        break;
      case 'VOUCHER_ALREADY_USED':
        showError('Anda sudah menggunakan voucher ini');
        break;
      default:
        showError('Gagal menerapkan voucher');
    }
  }
};
```

### Remove Voucher
```typescript
const removeVoucher = async () => {
  const { cartId } = useCartStore.getState();
  await api.delete(`/api/v1/carts/${cartId}/voucher`);
  await getCart(); // Refresh cart
};
```

---

## 📦 Order Tracking

### List Orders
```typescript
const fetchOrders = async (page = 1) => {
  const { data } = await api.get('/api/v1/orders', {
    params: { page, limit: 10 },
  });
  return {
    orders: data.data,
    pagination: data.pagination,
  };
};
```

### Track Shipment
```typescript
const trackShipment = async (orderId: string) => {
  const { data } = await api.get(`/api/v1/orders/${orderId}/shipment`);
  
  return {
    trackingNumber: data.data.trackingNumber,
    status: data.data.status,
    tracking: data.data.tracking, // Array of tracking events
  };
};
```

---

## 💡 Helper Functions

### Format Currency
```typescript
export const formatCurrency = (amount: number) => {
  return new Intl.NumberFormat('id-ID', {
    style: 'currency',
    currency: 'IDR',
    minimumFractionDigits: 0,
  }).format(amount);
};

// Usage: formatCurrency(12000000) => "Rp 12.000.000"
```

### Format Date
```typescript
export const formatDate = (date: string) => {
  return new Intl.DateTimeFormat('id-ID', {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  }).format(new Date(date));
};

// Usage: formatDate("2025-12-07T10:00:00Z") => "7 Desember 2025"
```

### Order Status Labels
```typescript
export const ORDER_STATUS_LABELS = {
  pending_payment: {
    label: 'Menunggu Pembayaran',
    color: 'yellow',
    icon: 'clock',
  },
  paid: {
    label: 'Dibayar',
    color: 'blue',
    icon: 'check',
  },
  processing: {
    label: 'Diproses',
    color: 'blue',
    icon: 'box',
  },
  shipped: {
    label: 'Dikirim',
    color: 'purple',
    icon: 'truck',
  },
  delivered: {
    label: 'Selesai',
    color: 'green',
    icon: 'check-circle',
  },
  cancelled: {
    label: 'Dibatalkan',
    color: 'red',
    icon: 'x-circle',
  },
};
```

---

## 🐛 Error Handling Best Practices

### Centralized Error Handler
```typescript
export const handleApiError = (error: any) => {
  const errorData = error.response?.data?.error;
  
  if (!errorData) {
    return 'Terjadi kesalahan, silakan coba lagi';
  }
  
  // Custom messages berdasarkan error code
  const errorMessages: Record<string, string> = {
    UNAUTHORIZED: 'Sesi Anda telah berakhir, silakan login kembali',
    OUT_OF_STOCK: 'Maaf, produk tidak tersedia',
    CART_EXPIRED: 'Keranjang Anda telah expired, silakan mulai belanja lagi',
    RATE_LIMIT_EXCEEDED: 'Terlalu banyak percobaan, silakan tunggu sebentar',
  };
  
  return errorMessages[errorData.code] || errorData.message;
};

// Usage
try {
  await addToCart(productId, qty);
} catch (error) {
  toast.error(handleApiError(error));
}
```

---

## 🧪 Testing

### Mock API untuk Development
```typescript
// lib/mock-api.ts
export const mockProducts = [
  {
    id: 'uuid-1',
    title: 'Samsung Galaxy S24',
    slug: 'samsung-galaxy-s24',
    price: 12000000,
    imageUrl: '/images/products/s24.jpg',
    // ... other fields
  },
];

// Gunakan saat development
const useMockData = process.env.NODE_ENV === 'development' && 
                    process.env.NEXT_PUBLIC_USE_MOCK === 'true';

export const fetchProducts = async (filters: ProductFilters) => {
  if (useMockData) {
    return { products: mockProducts, pagination: { ... } };
  }
  
  return api.get('/api/v1/products', { params: filters });
};
```

---

## 📊 Performance Optimization

### React Query Setup
```typescript
// lib/react-query.ts
import { QueryClient } from '@tanstack/react-query';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 60 * 1000, // 1 minute
      cacheTime: 5 * 60 * 1000, // 5 minutes
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

// Usage dengan React Query
import { useQuery } from '@tanstack/react-query';

export const useProducts = (filters: ProductFilters) => {
  return useQuery({
    queryKey: ['products', filters],
    queryFn: () => fetchProducts(filters),
  });
};

export const useCart = () => {
  const { cartId } = useCartStore();
  
  return useQuery({
    queryKey: ['cart', cartId],
    queryFn: () => getCart(),
    enabled: !!cartId,
    staleTime: 30 * 1000, // 30 seconds
  });
};
```

### Infinite Scroll untuk Product List
```typescript
import { useInfiniteQuery } from '@tanstack/react-query';

export const useInfiniteProducts = (filters: ProductFilters) => {
  return useInfiniteQuery({
    queryKey: ['products', filters],
    queryFn: ({ pageParam = 1 }) => 
      fetchProducts({ ...filters, page: pageParam }),
    getNextPageParam: (lastPage) => {
      const { page, perPage, totalItems } = lastPage.pagination;
      const hasMore = page * perPage < totalItems;
      return hasMore ? page + 1 : undefined;
    },
  });
};
```

---

## 🔔 Real-time Updates (Optional)

Jika backend menambahkan WebSocket untuk real-time order updates:

```typescript
import { useEffect } from 'react';
import { io } from 'socket.io-client';

export const useOrderUpdates = (orderId: string) => {
  useEffect(() => {
    const socket = io(process.env.NEXT_PUBLIC_WS_URL);
    
    socket.emit('subscribe', { orderId });
    
    socket.on('order:updated', (data) => {
      // Update order status di UI
      queryClient.invalidateQueries(['order', orderId]);
    });
    
    return () => {
      socket.disconnect();
    };
  }, [orderId]);
};
```

---

## 📝 Checklist Integrasi

- [ ] Setup API client dengan interceptors
- [ ] Implement authentication flow (login, register, logout)
- [ ] Setup auto-refresh token
- [ ] Implement guest cart management
- [ ] Implement cart merge setelah login
- [ ] Implement product listing dengan filter
- [ ] Implement product search dengan debounce
- [ ] Implement add to cart
- [ ] Implement voucher apply/remove
- [ ] Implement checkout flow
- [ ] Implement order list & detail
- [ ] Implement shipment tracking
- [ ] Setup error handling
- [ ] Setup loading states
- [ ] Setup React Query untuk caching
- [ ] Testing semua flow (guest → login → checkout)

---

## 🚀 Next Steps

1. **Setup project:** Install dependencies dan configure API client
2. **Test API:** Test semua endpoint dengan Postman/Insomnia
3. **Build features:** Ikuti checklist di atas
4. **Testing:** Test guest flow dan authenticated flow
5. **Optimization:** Setup React Query dan optimistic updates

---

## 📞 Need Help?

- **API Documentation:** `/docs/API_CONTRACT.md`
- **Backend Team:** #backend-support di Slack
- **Bug Reports:** GitHub Issues

**Happy Coding! 🎉**
