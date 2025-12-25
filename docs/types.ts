/**
 * TypeScript Type Definitions untuk Toko API
 * 
 * Copy file ini ke project frontend Anda dan import types yang diperlukan
 * 
 * @version 0.2.0
 * @lastUpdated 2025-12-07
 */

// ============================================================================
// Common Types
// ============================================================================

export interface ApiResponse<T> {
  data: T;
}

export interface ApiError {
  error: {
    code: string;
    message: string;
    details?: Record<string, any>;
  };
}

export interface Pagination {
  page: number;
  perPage: number;
  totalItems: number;
}

export interface PaginatedResponse<T> {
  data: T[];
  pagination: Pagination;
}

// ============================================================================
// Authentication & User Types
// ============================================================================

export interface User {
  id: string;
  name: string;
  email: string;
  emailVerified: boolean;
  createdAt: string;
  updatedAt?: string;
}

export interface RegisterRequest {
  name: string;
  email: string;
  password: string;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface AuthResponse {
  user: User;
  accessToken: string;
}

export interface RefreshTokenResponse {
  accessToken: string;
}

export interface ForgotPasswordRequest {
  email: string;
}

export interface ResetPasswordRequest {
  token: string;
  newPassword: string;
}

// ============================================================================
// Address Types
// ============================================================================

export interface Address {
  id: string;
  label: string;
  receiverName: string;
  phone: string;
  country: string;
  province: string;
  city: string;
  postalCode: string;
  addressLine1: string;
  addressLine2: string;
  isDefault: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface CreateAddressRequest {
  label: string;
  receiver_name: string;
  phone: string;
  country: string;
  province: string;
  city: string;
  postal_code: string;
  address_line1: string;
  address_line2?: string;
  is_default?: boolean;
}

export interface UpdateAddressRequest extends Partial<CreateAddressRequest> {}

// ============================================================================
// Catalog Types
// ============================================================================

export interface Category {
  id: string;
  name: string;
  slug: string;
  description?: string;
  imageUrl?: string;
  parentId?: string;
  createdAt: string;
}

export interface Brand {
  id: string;
  name: string;
  slug: string;
  logoUrl?: string;
  description?: string;
  createdAt: string;
}

export interface ProductImage {
  url: string;
  alt: string;
  isPrimary: boolean;
}

export interface ProductVariant {
  id: string;
  name: string;
  sku: string;
  price: number;
  stock: number;
  attributes: Record<string, string>;
}

export interface Product {
  id: string;
  title: string;
  slug: string;
  description: string;
  price: number;
  originalPrice?: number;
  discountPercent?: number;
  currency: string;
  categoryId: string;
  categoryName: string;
  brandId?: string;
  brandName?: string;
  imageUrl: string;
  images?: string[];
  stock: number;
  inStock: boolean;
  rating?: number;
  reviewCount?: number;
  tags?: string[];
  createdAt: string;
}

export interface ProductDetail extends Product {
  category: {
    id: string;
    name: string;
    slug: string;
  };
  brand?: {
    id: string;
    name: string;
    slug: string;
  };
  images: ProductImage[];
  variants?: ProductVariant[];
  specifications?: Record<string, string>;
  weight?: number;
  dimensions?: string;
  updatedAt: string;
}

export type ProductSortOption = 
  | 'price:asc'
  | 'price:desc'
  | 'title:asc'
  | 'title:desc';

export interface ProductFilters {
  q?: string;
  category?: string;
  brand?: string;
  minPrice?: number;
  maxPrice?: number;
  inStock?: boolean;
  sort?: ProductSortOption;
  page?: number;
  limit?: number;
}

// ============================================================================
// Cart Types
// ============================================================================

export interface CartItem {
  id: string;
  productId: string;
  variantId?: string | null;
  title: string;
  slug: string;
  qty: number;
  unitPrice: number;
  subtotal: number;
  imageUrl?: string;
}

export interface CartPricing {
  subtotal: number;
  discount: number;
  tax: number;
  shipping: number;
  total: number;
}

export interface Cart {
  id: string;
  anonId?: string | null;
  voucher?: string | null;
  items: CartItem[];
  pricing: CartPricing;
  currency: string;
}

export interface CreateCartRequest {
  anonId?: string;
}

export interface CreateCartResponse {
  cartId: string;
  anonId: string;
  voucher: string | null;
}

export interface AddCartItemRequest {
  productId: string;
  variantId?: string | null;
  qty: number;
}

export interface UpdateCartItemRequest {
  qty: number;
}

export interface ApplyVoucherRequest {
  code: string;
}

export interface ApplyVoucherResponse {
  discount: number;
}

export interface MergeCartRequest {
  cartId: string;
}

export interface MergeCartResponse {
  cartId: string;
}

// ============================================================================
// Shipping Types
// ============================================================================

export interface ShippingRate {
  service: string;
  description: string;
  cost: number;
  etd: string;
  note?: string;
}

export interface ShippingQuoteRequest {
  destination: string;
  courier: string;
  weightGram: number;
}

export type CourierCode = 'jne' | 'pos' | 'tiki' | 'sicepat' | 'jnt';

export interface TaxQuoteResponse {
  tax: number;
}

// ============================================================================
// Order Types
// ============================================================================

export type OrderStatus = 
  | 'pending_payment'
  | 'paid'
  | 'processing'
  | 'shipped'
  | 'delivered'
  | 'cancelled';

export type PaymentMethod = 
  | 'bank_transfer'
  | 'virtual_account'
  | 'credit_card'
  | 'ewallet_gopay'
  | 'ewallet_ovo'
  | 'ewallet_dana';

export interface OrderItem {
  id: string;
  productId: string;
  productTitle: string;
  productSlug: string;
  variantName?: string;
  qty: number;
  unitPrice: number;
  subtotal: number;
  imageUrl?: string;
}

export interface OrderShippingAddress {
  receiverName: string;
  phone: string;
  addressLine1: string;
  addressLine2?: string;
  city: string;
  province: string;
  postalCode: string;
  country: string;
}

export interface OrderPricing {
  subtotal: number;
  discount: number;
  tax: number;
  shipping: number;
  total: number;
}

export interface OrderVoucher {
  code: string;
  discount: number;
}

export interface OrderShipping {
  courier: string;
  service: string;
  trackingNumber?: string;
  estimatedDelivery?: string;
  shippedAt?: string;
}

export interface OrderPayment {
  method: PaymentMethod;
  methodLabel: string;
  status: 'pending' | 'paid' | 'failed';
  paidAt?: string;
  paymentUrl?: string;
  expiryAt?: string;
}

export interface OrderStatusHistory {
  status: OrderStatus;
  timestamp: string;
}

export interface Order {
  id: string;
  orderNumber: string;
  status: OrderStatus;
  statusLabel: string;
  total: number;
  currency: string;
  itemCount: number;
  thumbnailUrl?: string;
  paymentMethod: PaymentMethod;
  createdAt: string;
  updatedAt: string;
}

export interface OrderDetail extends Order {
  user: {
    id: string;
    name: string;
    email: string;
  };
  items: OrderItem[];
  shippingAddress: OrderShippingAddress;
  pricing: OrderPricing;
  voucher?: OrderVoucher;
  shipping?: OrderShipping;
  payment: OrderPayment;
  notes?: string;
  statusHistory: OrderStatusHistory[];
}

export interface CheckoutRequest {
  cartId: string;
  shippingAddressId: string;
  shippingService: string;
  shippingCost: number;
  paymentMethod: PaymentMethod;
  notes?: string;
}

export interface CheckoutResponse {
  orderId: string;
  orderNumber: string;
  status: OrderStatus;
  total: number;
  currency: string;
  paymentMethod: PaymentMethod;
  paymentUrl?: string;
  paymentExpiry?: string;
  createdAt: string;
}

export interface CancelOrderResponse {
  orderId: string;
  status: OrderStatus;
  message: string;
}

// ============================================================================
// Shipment Tracking Types
// ============================================================================

export type ShipmentStatus = 
  | 'pending'
  | 'picked_up'
  | 'in_transit'
  | 'on_delivery'
  | 'delivered'
  | 'failed';

export interface TrackingEvent {
  timestamp: string;
  status: ShipmentStatus;
  location: string;
  description: string;
}

export interface Shipment {
  orderId: string;
  trackingNumber: string;
  courier: string;
  service: string;
  status: ShipmentStatus;
  statusLabel: string;
  estimatedDelivery?: string;
  shippedAt: string;
  tracking: TrackingEvent[];
}

// ============================================================================
// Voucher Types (Admin)
// ============================================================================

export type VoucherType = 'percentage' | 'fixed';

export interface CreateVoucherRequest {
  code: string;
  type: VoucherType;
  value: number;
  minSpend?: number;
  maxDiscount?: number;
  usageLimit?: number;
  perUserLimit?: number;
  validFrom: string;
  validUntil: string;
  description?: string;
}

export interface Voucher extends CreateVoucherRequest {
  id: string;
  usedCount: number;
  isActive: boolean;
  createdAt: string;
  updatedAt: string;
}

// ============================================================================
// Health Check Types
// ============================================================================

export interface HealthCheck {
  status: 'ok' | 'degraded' | 'down';
}

export interface ReadinessCheck {
  status: 'ready' | 'not_ready';
  checks: {
    database: 'ok' | 'error';
    redis: 'ok' | 'error';
  };
}

// ============================================================================
// Rate Limit Types
// ============================================================================

export interface RateLimitHeaders {
  'X-RateLimit-Limit': string;
  'X-RateLimit-Remaining': string;
  'X-RateLimit-Reset': string;
}

// ============================================================================
// Webhook Types (Payment Gateway)
// ============================================================================

export interface PaymentWebhookPayload {
  transaction_id: string;
  order_id: string;
  payment_type: string;
  transaction_status: 'pending' | 'settlement' | 'expire' | 'cancel';
  gross_amount: string;
}

// ============================================================================
// Query Types (React Query)
// ============================================================================

export interface UseProductsResult {
  products: Product[];
  pagination: Pagination;
  isLoading: boolean;
  error: Error | null;
}

export interface UseCartResult {
  cart: Cart | null;
  isLoading: boolean;
  error: Error | null;
  addItem: (productId: string, qty: number, variantId?: string) => Promise<void>;
  updateQty: (itemId: string, qty: number) => Promise<void>;
  removeItem: (itemId: string) => Promise<void>;
  applyVoucher: (code: string) => Promise<void>;
  removeVoucher: () => Promise<void>;
}

export interface UseOrdersResult {
  orders: Order[];
  pagination: Pagination;
  isLoading: boolean;
  error: Error | null;
}

// ============================================================================
// Error Code Types
// ============================================================================

export type ApiErrorCode = 
  | 'UNAUTHORIZED'
  | 'FORBIDDEN'
  | 'NOT_FOUND'
  | 'BAD_REQUEST'
  | 'VALIDATION_ERROR'
  | 'INTERNAL'
  | 'UNAVAILABLE'
  | 'CART_EXPIRED'
  | 'OUT_OF_STOCK'
  | 'VOUCHER_INVALID'
  | 'VOUCHER_MIN_SPEND'
  | 'VOUCHER_ALREADY_USED'
  | 'RATE_LIMIT_EXCEEDED';

// ============================================================================
// Local Storage Types
// ============================================================================

export interface CartStorage {
  cartId: string | null;
  anonId: string | null;
}

export interface AuthStorage {
  accessToken: string | null;
}

// ============================================================================
// Status Label Maps (for UI)
// ============================================================================

export interface StatusLabel {
  label: string;
  color: 'yellow' | 'blue' | 'purple' | 'green' | 'red';
  icon: string;
}

export const ORDER_STATUS_LABELS: Record<OrderStatus, StatusLabel> = {
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

export const SHIPMENT_STATUS_LABELS: Record<ShipmentStatus, StatusLabel> = {
  pending: {
    label: 'Menunggu Pengiriman',
    color: 'yellow',
    icon: 'clock',
  },
  picked_up: {
    label: 'Diambil Kurir',
    color: 'blue',
    icon: 'box',
  },
  in_transit: {
    label: 'Dalam Perjalanan',
    color: 'blue',
    icon: 'truck',
  },
  on_delivery: {
    label: 'Sedang Diantar',
    color: 'purple',
    icon: 'map-pin',
  },
  delivered: {
    label: 'Terkirim',
    color: 'green',
    icon: 'check-circle',
  },
  failed: {
    label: 'Gagal Dikirim',
    color: 'red',
    icon: 'x-circle',
  },
};

// ============================================================================
// Utility Types
// ============================================================================

export type Nullable<T> = T | null;
export type Optional<T> = T | undefined;

// Deep partial type for update requests
export type DeepPartial<T> = {
  [P in keyof T]?: T[P] extends object ? DeepPartial<T[P]> : T[P];
};

// API request config
export interface ApiRequestConfig {
  headers?: Record<string, string>;
  params?: Record<string, any>;
  timeout?: number;
}

// ============================================================================
// Export all types
// ============================================================================

export type {
  ApiResponse,
  ApiError,
  Pagination,
  PaginatedResponse,
};
