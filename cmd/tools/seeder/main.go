package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/lib/pq"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("Failed to close DB: %v", closeErr)
		}
	}()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping DB: %v", err)
	}

	// 1. Get or Create Default Tenant
	var tenantID string
	err = db.QueryRow("SELECT id FROM tenants WHERE slug = 'default'").Scan(&tenantID)
	if err != nil {
		log.Println("Default tenant not found, attempting to create...")
		err = db.QueryRow(`
			INSERT INTO tenants (name, slug) VALUES ('Toko Kios', 'default')
			ON CONFLICT (slug) DO UPDATE SET name=EXCLUDED.name
			RETURNING id;
		`).Scan(&tenantID)
		if err != nil {
			log.Fatalf("Failed to retrieve or create default tenant: %v", err)
		}
	}
	log.Printf("Using Tenant ID: %s", tenantID)

	// 2. Run Seeders
	userIDs := seedUsers(db, tenantID)
	_, _, productIDs, variantMap := seedCatalog(db, tenantID)
	seedAddresses(db, userIDs)
	seedVouchers(db, tenantID)
	seedFlashSales(db, tenantID, productIDs)
	seedReviews(db, tenantID, userIDs, productIDs)
	seedProductQuestions(db, tenantID, userIDs, productIDs)
	seedOrders(db, tenantID, userIDs, productIDs, variantMap)
	seedSupportTickets(db, tenantID, userIDs)
	seedLoyalty(db, userIDs)

	log.Println("🎉 All demonstration seed data populated successfully!")
}

// -------------------------------------------------------------
// USERS & TENANT MEMBERSHIPS
// -------------------------------------------------------------
func seedUsers(db *sql.DB, tenantID string) map[string]string {
	fmt.Println("Seeding Users & Tenant Memberships...")

	// Password: "password123" (Argon2id)
	argonHash := "$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I"

	users := []struct {
		Name  string
		Email string
		Role  string
		Phone string
	}{
		{"Admin Toko", "admin@toko.com", "admin", "081211112222"},
		{"Noah Developer", "noah@toko.com", "admin", "081299998888"},
		{"Budi Santoso", "budi@example.com", "customer", "081234567890"},
		{"Siti Aminah", "siti@example.com", "customer", "081345678901"},
		{"Andi Pratama", "andi@example.com", "customer", "081456789012"},
		{"Dewi Lestari", "dewi@example.com", "customer", "081567890123"},
		{"Eko Kurniawan", "eko@example.com", "customer", "081678901234"},
		{"Fajar Nugraha", "fajar@example.com", "customer", "081789012345"},
		{"Gita Pertiwi", "gita@example.com", "customer", "081890123456"},
		{"Hendra Wijaya", "hendra@example.com", "customer", "081901234567"},
		{"Indah Sari", "indah@example.com", "customer", "082112345678"},
		{"Joko Widodo", "joko@example.com", "customer", "082223456789"},
		{"Rina Anggraini", "rina@example.com", "customer", "082334567890"},
		{"Dimas Arya", "dimas@example.com", "customer", "082445678901"},
		{"Maya Putri", "maya@example.com", "customer", "082556789012"},
	}

	userMap := make(map[string]string)
	for _, u := range users {
		var id string
		err := db.QueryRow(`
			INSERT INTO users (name, email, password_hash, roles, phone, email_verified_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, NOW(), NOW() - INTERVAL '60 days', NOW())
			ON CONFLICT (email) DO UPDATE SET
				name = EXCLUDED.name,
				password_hash = EXCLUDED.password_hash,
				roles = EXCLUDED.roles,
				phone = EXCLUDED.phone,
				email_verified_at = NOW(),
				updated_at = NOW()
			RETURNING id;
		`, u.Name, u.Email, argonHash, pq.Array([]string{u.Role}), u.Phone).Scan(&id)
		if err != nil {
			log.Printf("Failed to seed user %s: %v", u.Email, err)
			continue
		}
		userMap[u.Email] = id

		// Assign Tenant Membership
		membershipRole := "CUSTOMER"
		if u.Role == "admin" {
			membershipRole = "OWNER"
		}
		_, err = db.Exec(`
			INSERT INTO tenant_memberships (tenant_id, user_id, role, status)
			VALUES ($1, $2, $3, 'ACTIVE')
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role, status = 'ACTIVE';
		`, tenantID, id, membershipRole)
		if err != nil {
			log.Printf("Failed to seed tenant membership for %s: %v", u.Email, err)
		}
	}
	return userMap
}

// -------------------------------------------------------------
// CATALOG (CATEGORIES, BRANDS, PRODUCTS, VARIANTS)
// -------------------------------------------------------------
type VariantSeed struct {
	SKU   string
	Price int64
	Stock int
}

type ProductSeed struct {
	Title       string
	Slug        string
	Brand       string
	Category    string
	Price       int64
	CompareAt   int64
	Image       string
	Badges      []string
	Description string
	Variants    []VariantSeed
}

func seedCatalog(db *sql.DB, tenantID string) (map[string]string, map[string]string, map[string]string, map[string][]VariantSeed) {
	fmt.Println("Seeding Catalog (Categories, Brands, Products, Variants)...")

	// 1. Categories
	categories := []struct {
		Name string
		Slug string
	}{
		{"Elektronik & Gadget", "electronics"},
		{"Fashion Pria & Wanita", "fashion"},
		{"Perlengkapan Rumah", "home-living"},
		{"Kecantikan & Perawatan", "beauty"},
		{"Olahraga & Outdoor", "sports"},
		{"Mainan & Hobi", "toys"},
		{"Buku & Alat Tulis", "books"},
		{"Otomotif & Aksesoris", "automotive"},
		{"Kesehatan", "health"},
		{"Makanan & Minuman", "food-beverage"},
	}

	catIDs := make(map[string]string)
	for _, c := range categories {
		var id string
		err := db.QueryRow(`
			INSERT INTO categories (name, slug, tenant_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
			RETURNING id;
		`, c.Name, c.Slug, tenantID).Scan(&id)
		if err != nil {
			_ = db.QueryRow("SELECT id FROM categories WHERE slug = $1", c.Slug).Scan(&id)
		}
		catIDs[c.Slug] = id
	}

	// 2. Brands
	brands := []struct {
		Name string
		Slug string
	}{
		{"Apple", "apple"},
		{"Samsung", "samsung"},
		{"Sony", "sony"},
		{"Xiaomi", "xiaomi"},
		{"Asus ROG", "asus"},
		{"Logitech", "logitech"},
		{"Nike", "nike"},
		{"Adidas", "adidas"},
		{"Erigo", "erigo"},
		{"Eiger", "eiger"},
		{"Uniqlo", "uniqlo"},
		{"IKEA", "ikea"},
		{"Dyson", "dyson"},
		{"Philips", "philips"},
		{"Somethinc", "somethinc"},
		{"Scarlett", "scarlett"},
		{"Wardah", "wardah"},
		{"LEGO", "lego"},
		{"Gramedia", "gramedia"},
	}

	brandIDs := make(map[string]string)
	for _, b := range brands {
		var id string
		err := db.QueryRow(`
			INSERT INTO brands (name, slug, tenant_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
			RETURNING id;
		`, b.Name, b.Slug, tenantID).Scan(&id)
		if err != nil {
			_ = db.QueryRow("SELECT id FROM brands WHERE slug = $1", b.Slug).Scan(&id)
		}
		brandIDs[b.Slug] = id
	}

	// 3. Products
	products := []ProductSeed{
		{
			Title:       "Apple MacBook Pro 14 M3",
			Slug:        "macbook-pro-14-m3",
			Brand:       "apple",
			Category:    "electronics",
			Price:       25999000,
			CompareAt:   28999000,
			Image:       "https://images.unsplash.com/photo-1517336714731-489689fd1ca8?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"new", "bestseller"},
			Description: "Laptop MacBook Pro 14 inci dengan chip Apple M3, Liquid Retina XDR display, dan baterai hingga 18 jam.",
			Variants: []VariantSeed{
				{SKU: "MBP14-M3-512GB-SPACE", Price: 25999000, Stock: 25},
				{SKU: "MBP14-M3-1TB-SILVER", Price: 29999000, Stock: 15},
			},
		},
		{
			Title:       "Apple iPhone 15 Pro Titanium",
			Slug:        "iphone-15-pro",
			Brand:       "apple",
			Category:    "electronics",
			Price:       18999000,
			CompareAt:   20999000,
			Image:       "https://images.unsplash.com/photo-1695048133142-1a20484d2569?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller"},
			Description: "iPhone 15 Pro dengan bodi titanium aerospace-grade, chip A17 Pro, kamera 48MP, dan Action Button.",
			Variants: []VariantSeed{
				{SKU: "IPH15P-128GB-NATURAL", Price: 18999000, Stock: 40},
				{SKU: "IPH15P-256GB-BLUE", Price: 21499000, Stock: 30},
				{SKU: "IPH15P-512GB-BLACK", Price: 25499000, Stock: 10},
			},
		},
		{
			Title:       "Samsung Galaxy S24 Ultra 5G",
			Slug:        "samsung-galaxy-s24-ultra",
			Brand:       "samsung",
			Category:    "electronics",
			Price:       19999000,
			CompareAt:   21999000,
			Image:       "https://images.unsplash.com/photo-1610945415295-d9bbf067e59c?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"promo"},
			Description: "Smartphone flagship dengan Galaxy AI terintegrasi, stylus S-Pen, dan kamera 200MP Nightography.",
			Variants: []VariantSeed{
				{SKU: "S24U-256GB-TITAN", Price: 19999000, Stock: 35},
				{SKU: "S24U-512GB-VIOLET", Price: 21999000, Stock: 20},
			},
		},
		{
			Title:       "Sony WH-1000XM5 Wireless Headphone",
			Slug:        "sony-wh-1000xm5",
			Brand:       "sony",
			Category:    "electronics",
			Price:       4999000,
			CompareAt:   5999000,
			Image:       "https://images.unsplash.com/photo-1618366712010-f4ae9c647dcb?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller", "promo"},
			Description: "Headphone over-ear peredam bising terbaik di kelasnya dengan prosesor terintegrasi V1 dan 8 mikrofon.",
			Variants: []VariantSeed{
				{SKU: "WH1000XM5-BLACK", Price: 4999000, Stock: 50},
				{SKU: "WH1000XM5-SILVER", Price: 4999000, Stock: 30},
			},
		},
		{
			Title:       "Sony PlayStation 5 Slim Digital & Disc",
			Slug:        "sony-ps5-slim",
			Brand:       "sony",
			Category:    "toys",
			Price:       8999000,
			CompareAt:   9699000,
			Image:       "https://images.unsplash.com/photo-1606813907291-d86efa9b94db?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller"},
			Description: "Konsol gaming generasi terbaru PS5 Slim dengan penyimpanan 1TB SSD ultra-cepat dan DualSense haptic feedback.",
			Variants: []VariantSeed{
				{SKU: "PS5-SLIM-DISC", Price: 8999000, Stock: 25},
				{SKU: "PS5-SLIM-DIGITAL", Price: 7499000, Stock: 20},
			},
		},
		{
			Title:       "Nike Air Force 1 '07 Triple White",
			Slug:        "nike-air-force-1-07",
			Brand:       "nike",
			Category:    "fashion",
			Price:       1549000,
			CompareAt:   1799000,
			Image:       "https://images.unsplash.com/photo-1542291026-7eec264c27ff?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller"},
			Description: "Sepatu ikonik streetwear berbahan kulit premium dengan bantalan Nike Air yang empuk dan nyaman seharian.",
			Variants: []VariantSeed{
				{SKU: "AF1-WHT-40", Price: 1549000, Stock: 30},
				{SKU: "AF1-WHT-41", Price: 1549000, Stock: 40},
				{SKU: "AF1-WHT-42", Price: 1549000, Stock: 45},
				{SKU: "AF1-WHT-43", Price: 1549000, Stock: 25},
			},
		},
		{
			Title:       "Adidas Ultraboost Light Running Shoes",
			Slug:        "adidas-ultraboost-light",
			Brand:       "adidas",
			Category:    "sports",
			Price:       2499000,
			CompareAt:   3000000,
			Image:       "https://images.unsplash.com/photo-1608231387042-66d1773070a5?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"promo"},
			Description: "Sepatu lari terobosan dengan material Light BOOST 30% lebih ringan, responsif, dan stabil di setiap langkah.",
			Variants: []VariantSeed{
				{SKU: "UB-LIGHT-BLK-41", Price: 2499000, Stock: 20},
				{SKU: "UB-LIGHT-BLK-42", Price: 2499000, Stock: 25},
				{SKU: "UB-LIGHT-GRY-42", Price: 2499000, Stock: 15},
			},
		},
		{
			Title:       "Erigo Sukajan Jacket Dragon & Phoenix",
			Slug:        "erigo-sukajan-dragon",
			Brand:       "erigo",
			Category:    "fashion",
			Price:       399000,
			CompareAt:   650000,
			Image:       "https://images.unsplash.com/photo-1551028719-00167b16eac5?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"promo", "local-pride"},
			Description: "Jaket Sukajan khas Jepang dengan bordir detail naga legendaris berbahan satin premium lembut dan adem.",
			Variants: []VariantSeed{
				{SKU: "ERG-SKJ-M", Price: 399000, Stock: 60},
				{SKU: "ERG-SKJ-L", Price: 399000, Stock: 80},
				{SKU: "ERG-SKJ-XL", Price: 399000, Stock: 50},
			},
		},
		{
			Title:       "Eiger Diario 28L Adventure Backpack",
			Slug:        "eiger-diario-28l",
			Brand:       "eiger",
			Category:    "sports",
			Price:       549000,
			CompareAt:   629000,
			Image:       "https://images.unsplash.com/photo-1553062407-98eeb64c6a62?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller"},
			Description: "Tas ransel tangguh tahan percikan air cocok untuk aktivitas harian, kuliah, maupun hiking ringan.",
			Variants: []VariantSeed{
				{SKU: "EIG-DIA28-BLK", Price: 549000, Stock: 70},
				{SKU: "EIG-DIA28-OLV", Price: 549000, Stock: 40},
			},
		},
		{
			Title:       "IKEA STRANDMON Wing Chair",
			Slug:        "ikea-strandmon-chair",
			Brand:       "ikea",
			Category:    "home-living",
			Price:       2995000,
			CompareAt:   3499000,
			Image:       "https://images.unsplash.com/photo-1555041469-a586c61ea9bc?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller"},
			Description: "Kursi berlengan klasik dengan sandaran tinggi memberikan topangan ekstra nyaman bagi leher dan punggung.",
			Variants: []VariantSeed{
				{SKU: "IKEA-STR-YEL", Price: 2995000, Stock: 15},
				{SKU: "IKEA-STR-GRY", Price: 2995000, Stock: 18},
			},
		},
		{
			Title:       "Dyson V12 Detect Slim Cordless Vacuum",
			Slug:        "dyson-v12-detect-slim",
			Brand:       "dyson",
			Category:    "home-living",
			Price:       11999000,
			CompareAt:   13499000,
			Image:       "https://images.unsplash.com/photo-1558317374-067fb5f30001?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"new"},
			Description: "Penyedot debu tanpa kabel teringan dari Dyson dengan teknologi laser pendeteksi partikel debu tak kasat mata.",
			Variants: []VariantSeed{
				{SKU: "DYS-V12-SLIM", Price: 11999000, Stock: 20},
			},
		},
		{
			Title:       "Somethinc Niacinamide + Moisture Serum",
			Slug:        "somethinc-niacinamide-serum",
			Brand:       "somethinc",
			Category:    "beauty",
			Price:       115000,
			CompareAt:   145000,
			Image:       "https://images.unsplash.com/photo-1620916566398-39f1143ab7be?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller", "local-pride"},
			Description: "Serum pencerah wajah dengan Niacinamide dan ekstrak Beetroot untuk samarkan noda hitam dan hidrasi kulit.",
			Variants: []VariantSeed{
				{SKU: "SMT-NIA-20ML", Price: 115000, Stock: 200},
				{SKU: "SMT-NIA-40ML", Price: 195000, Stock: 150},
			},
		},
		{
			Title:       "Scarlett Whitening Body Lotion Romansa",
			Slug:        "scarlett-whitening-body-lotion",
			Brand:       "scarlett",
			Category:    "beauty",
			Price:       75000,
			CompareAt:   85000,
			Image:       "https://images.unsplash.com/photo-1598440947619-2c35fc9aa908?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller"},
			Description: "Lotion tubuh dengan Glutathione dan Vitamin E yang menutrisi serta mencerahkan kulit dengan aroma wangi mewah.",
			Variants: []VariantSeed{
				{SKU: "SCL-ROM-300ML", Price: 75000, Stock: 300},
				{SKU: "SCL-JOL-300ML", Price: 75000, Stock: 250},
			},
		},
		{
			Title:       "LEGO Star Wars Millennium Falcon 75257",
			Slug:        "lego-star-wars-millennium-falcon",
			Brand:       "lego",
			Category:    "toys",
			Price:       2799000,
			CompareAt:   3199000,
			Image:       "https://images.unsplash.com/photo-1585366119957-e9730b6d0f60?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller"},
			Description: "Set LEGO Star Wars ikonik berisi 1.351 keping bricks lengkap dengan 7 minifigures karakter favorit.",
			Variants: []VariantSeed{
				{SKU: "LEGO-SW-75257", Price: 2799000, Stock: 35},
			},
		},
		{
			Title:       "Logitech MX Master 3S Wireless Mouse",
			Slug:        "logitech-mx-master-3s",
			Brand:       "logitech",
			Category:    "electronics",
			Price:       1599000,
			CompareAt:   1899000,
			Image:       "https://images.unsplash.com/photo-1527864550417-7fd91fc51a46?w=800&auto=format&fit=crop&q=80",
			Badges:      []string{"bestseller"},
			Description: "Mouse ergonomis premium untuk produktivitas dengan klik senyap Quiet Clicks dan sensor 8K DPI.",
			Variants: []VariantSeed{
				{SKU: "LOG-MX3S-GRAPHITE", Price: 1599000, Stock: 60},
				{SKU: "LOG-MX3S-PALEPALE", Price: 1599000, Stock: 40},
			},
		},
	}

	productIDs := make(map[string]string)
	variantMap := make(map[string][]VariantSeed)

	for _, p := range products {
		catID, hasCat := catIDs[p.Category]
		brandID, hasBrand := brandIDs[p.Brand]
		if !hasCat || !hasBrand {
			log.Printf("Skipping product %s due to missing cat or brand", p.Slug)
			continue
		}

		var prodID string
		err := db.QueryRow(`
			INSERT INTO products (title, slug, brand_id, category_id, price, compare_at, in_stock, thumbnail, badges, description, tenant_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7, $8::text[], $9, $10, NOW() - INTERVAL '30 days', NOW())
			ON CONFLICT (slug) DO UPDATE SET
				title = EXCLUDED.title,
				price = EXCLUDED.price,
				compare_at = EXCLUDED.compare_at,
				brand_id = EXCLUDED.brand_id,
				category_id = EXCLUDED.category_id,
				thumbnail = EXCLUDED.thumbnail,
				badges = EXCLUDED.badges,
				description = EXCLUDED.description,
				tenant_id = EXCLUDED.tenant_id,
				updated_at = NOW()
			RETURNING id;
		`, p.Title, p.Slug, brandID, catID, p.Price, p.CompareAt, p.Image, pq.Array(p.Badges), p.Description, tenantID).Scan(&prodID)

		if err != nil {
			log.Printf("Failed to upsert product %s: %v", p.Slug, err)
			continue
		}
		productIDs[p.Slug] = prodID
		variantMap[p.Slug] = p.Variants

		// Seed Variants
		for _, v := range p.Variants {
			_, err = db.Exec(`
				INSERT INTO product_variants (product_id, sku, price, stock)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (sku) DO UPDATE SET
					price = EXCLUDED.price,
					stock = EXCLUDED.stock;
			`, prodID, v.SKU, v.Price, v.Stock)
			if err != nil {
				log.Printf("Failed to upsert variant %s: %v", v.SKU, err)
			}
		}
	}

	return catIDs, brandIDs, productIDs, variantMap
}

// -------------------------------------------------------------
// ADDRESSES
// -------------------------------------------------------------
func seedAddresses(db *sql.DB, userIDs map[string]string) {
	fmt.Println("Seeding Addresses...")

	addresses := []struct {
		Email        string
		Receiver     string
		Phone        string
		AddressLine1 string
		City         string
		PostalCode   string
	}{
		{"budi@example.com", "Budi Santoso", "081234567890", "Jl. Sudirman No. 45, Senayan", "Jakarta Selatan", "12190"},
		{"siti@example.com", "Siti Aminah", "081345678901", "Jl. Dago Asri No. 12", "Bandung", "40135"},
		{"andi@example.com", "Andi Pratama", "081456789012", "Jl. Pemuda No. 88, Gubeng", "Surabaya", "60271"},
		{"dewi@example.com", "Dewi Lestari", "081567890123", "Jl. Malioboro No. 23", "Yogyakarta", "55271"},
		{"eko@example.com", "Eko Kurniawan", "081678901234", "Jl. Gatot Subroto No. 5", "Medan", "20114"},
		{"fajar@example.com", "Fajar Nugraha", "081789012345", "Jl. Sunset Road No. 101, Seminyak", "Badung (Bali)", "80361"},
		{"gita@example.com", "Gita Pertiwi", "081890123456", "Jl. Dhoho No. 77", "Kediri", "64129"},
	}

	for _, a := range addresses {
		userID, ok := userIDs[a.Email]
		if !ok {
			continue
		}
		_, err := db.Exec(`
			INSERT INTO addresses (user_id, receiver_name, phone, address_line1, city, postal_code, is_default, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, NOW() - INTERVAL '45 days', NOW())
			ON CONFLICT DO NOTHING;
		`, userID, a.Receiver, a.Phone, a.AddressLine1, a.City, a.PostalCode)
		if err != nil {
			log.Printf("Failed to seed address for %s: %v", a.Email, err)
		}
	}
}

// -------------------------------------------------------------
// VOUCHERS
// -------------------------------------------------------------
func seedVouchers(db *sql.DB, tenantID string) {
	fmt.Println("Seeding Vouchers...")

	vouchers := []struct {
		Code       string
		Value      int64
		Kind       string
		MinSpend   int64
		UsageLimit int
	}{
		{"WELCOME50K", 50000, "fixed_amount", 100000, 1000},
		{"GRATISONGKIR", 20000, "fixed_amount", 50000, 5000},
		{"DISKON100K", 100000, "fixed_amount", 500000, 500},
		{"GAJIAN20", 20000, "fixed_amount", 150000, 2000},
		{"MEGASALE", 250000, "fixed_amount", 1000000, 100},
	}

	for _, v := range vouchers {
		_, err := db.Exec(`
			INSERT INTO vouchers (code, value, kind, min_spend, usage_limit, used_count, tenant_id, valid_from, valid_to, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 42, $6, NOW() - INTERVAL '10 days', NOW() + INTERVAL '180 days', NOW() - INTERVAL '10 days', NOW())
			ON CONFLICT (code) DO UPDATE SET
				value = EXCLUDED.value,
				min_spend = EXCLUDED.min_spend,
				usage_limit = EXCLUDED.usage_limit,
				valid_to = EXCLUDED.valid_to;
		`, v.Code, v.Value, v.Kind, v.MinSpend, v.UsageLimit, tenantID)
		if err != nil {
			log.Printf("Failed to seed voucher %s: %v", v.Code, err)
		}
	}
}

// -------------------------------------------------------------
// FLASH SALES
// -------------------------------------------------------------
func seedFlashSales(db *sql.DB, tenantID string, productIDs map[string]string) {
	fmt.Println("Seeding Flash Sales...")

	// 1. Active Flash Sale (Super Payday Sale)
	var activeID string
	err := db.QueryRow(`
		INSERT INTO flash_sale_campaigns (tenant_id, name, slug, status, starts_at, ends_at)
		VALUES ($1, 'Super Payday Flash Sale', 'super-payday-sale', 'ACTIVE', NOW() - INTERVAL '1 day', NOW() + INTERVAL '3 days')
		ON CONFLICT (tenant_id, slug) DO UPDATE SET status = 'ACTIVE', starts_at = NOW() - INTERVAL '1 day', ends_at = NOW() + INTERVAL '3 days'
		RETURNING id;
	`, tenantID).Scan(&activeID)

	if err == nil {
		activeItems := []struct {
			Slug      string
			SalePrice int64
			Stock     int
		}{
			{"sony-wh-1000xm5", 3999000, 20},
			{"erigo-sukajan-dragon", 299000, 50},
			{"somethinc-niacinamide-serum", 89000, 100},
			{"nike-air-force-1-07", 1299000, 30},
		}

		for _, item := range activeItems {
			prodID, ok := productIDs[item.Slug]
			if !ok {
				continue
			}
			_, _ = db.Exec(`
				INSERT INTO flash_sale_items (campaign_id, product_id, sale_price, stock_limit)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (campaign_id, product_id) DO UPDATE SET sale_price = EXCLUDED.sale_price, stock_limit = EXCLUDED.stock_limit;
			`, activeID, prodID, item.SalePrice, item.Stock)
		}
	}

	// 2. Scheduled Flash Sale (Weekend Gadget Special)
	var schedID string
	_ = db.QueryRow(`
		INSERT INTO flash_sale_campaigns (tenant_id, name, slug, status, starts_at, ends_at)
		VALUES ($1, 'Weekend Gadget Special', 'weekend-gadget-special', 'SCHEDULED', NOW() + INTERVAL '2 days', NOW() + INTERVAL '5 days')
		ON CONFLICT (tenant_id, slug) DO UPDATE SET status = 'SCHEDULED', starts_at = NOW() + INTERVAL '2 days', ends_at = NOW() + INTERVAL '5 days'
		RETURNING id;
	`, tenantID).Scan(&schedID)

	if schedID != "" {
		if prodID, ok := productIDs["samsung-galaxy-s24-ultra"]; ok {
			_, _ = db.Exec(`
				INSERT INTO flash_sale_items (campaign_id, product_id, sale_price, stock_limit)
				VALUES ($1, $2, 17999000, 15)
				ON CONFLICT (campaign_id, product_id) DO UPDATE SET sale_price = EXCLUDED.sale_price;
			`, schedID, prodID)
		}
	}
}

// -------------------------------------------------------------
// REVIEWS & RATINGS
// -------------------------------------------------------------
func seedReviews(db *sql.DB, tenantID string, userIDs map[string]string, productIDs map[string]string) {
	fmt.Println("Seeding Product Reviews...")

	reviews := []struct {
		Email   string
		Slug    string
		Rating  int
		Comment string
	}{
		{"budi@example.com", "macbook-pro-14-m3", 5, "Performa chip M3 luar biasa kencang! Render video 4K lancar tanpa panas, baterai awet banget seharian. Pengiriman aman bubble wrap tebal."},
		{"siti@example.com", "sony-wh-1000xm5", 5, "Noise cancelling-nya benar-benar juara dunia, suara pesawat & kendaraan langsung hening total. Sangat nyaman dipakai kerja berjam-jam."},
		{"andi@example.com", "nike-air-force-1-07", 5, "Produk 100% original, pas di kaki dan empuk banget. Desain timeless wajib punya!"},
		{"dewi@example.com", "somethinc-niacinamide-serum", 5, "Cocok banget di kulit sensitifku, bekas jerawat mulai pudar setelah 2 minggu pemakaian teratur. Pasti repurchase lagi!"},
		{"eko@example.com", "iphone-15-pro", 5, "Bodi titanium terasa sangat ringan dibanding 14 Pro sebelumnya. Kamera 48MP tajem banget hasilnya."},
		{"fajar@example.com", "erigo-sukajan-dragon", 4, "Bordiran naga detail dan rapi banget. Bahan satin halus adem. Ukuran L pas di badan TB 175 BB 70."},
		{"gita@example.com", "scarlett-whitening-body-lotion", 5, "Wanginya tahan lama seperti parfum mahal, kulit jadi halus dan cerah seketika."},
		{"hendra@example.com", "sony-ps5-slim", 5, "Akhirnya punya PS5! Packing super aman, garansi resmi Sony Indonesia terdaftar."},
		{"indah@example.com", "logitech-mx-master-3s", 5, "Mouse terbaik untuk desainer dan programmer. Scroll wheel elektromagnetik magspeed halus banget."},
		{"joko@example.com", "ikea-strandmon-chair", 4, "Kursi sangat empuk dan estetik ditaruh di sudut ruang tamu. Perakitan mudah dibantu manual book."},
	}

	for _, r := range reviews {
		userID, hasUser := userIDs[r.Email]
		prodID, hasProd := productIDs[r.Slug]
		if !hasUser || !hasProd {
			continue
		}

		_, err := db.Exec(`
			INSERT INTO reviews (product_id, user_id, rating, comment, tenant_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, NOW() - INTERVAL '15 days', NOW())
			ON CONFLICT (user_id, product_id) DO UPDATE SET
				rating = EXCLUDED.rating,
				comment = EXCLUDED.comment,
				updated_at = NOW();
		`, prodID, userID, r.Rating, r.Comment, tenantID)
		if err != nil {
			log.Printf("Failed to seed review for %s: %v", r.Slug, err)
		}
	}
}

// -------------------------------------------------------------
// PRODUCT QUESTIONS & ANSWERS (Q&A)
// -------------------------------------------------------------
func seedProductQuestions(db *sql.DB, tenantID string, userIDs map[string]string, productIDs map[string]string) {
	fmt.Println("Seeding Product Questions & Answers...")

	adminID := userIDs["admin@toko.com"]
	questions := []struct {
		UserEmail string
		Slug      string
		Question  string
		Answer    string
	}{
		{
			"budi@example.com",
			"macbook-pro-14-m3",
			"Apakah ini garansi resmi iBox / Apple Indonesia?",
			"Halo Kak Budi, betul sekali! Semua unit yang kami jual 100% baru, segel resmi, dan bergaransi resmi Apple Authorized Indonesia (iBox/Digimap) selama 1 tahun.",
		},
		{
			"siti@example.com",
			"nike-air-force-1-07",
			"Untuk ukuran sepatu ini apakah true to size atau disarankan up size?",
			"Halo Kak Siti, untuk model Nike Air Force 1 '07 ukurannya True to Size (sesuai ukuran normal kaki). Namun bila memiliki tipe kaki lebar, bisa naik 0.5 size untuk kenyamanan optimal.",
		},
		{
			"andi@example.com",
			"sony-wh-1000xm5",
			"Apakah bisa konek ke 2 perangkat bersamaan (Multipoint Connection)?",
			"Halo Kak Andi, ya tentu bisa! Sony WH-1000XM5 mendukung Multipoint Connection sehingga bisa terhubung ke laptop dan smartphone secara simultan.",
		},
	}

	for _, q := range questions {
		userID, hasUser := userIDs[q.UserEmail]
		prodID, hasProd := productIDs[q.Slug]
		if !hasUser || !hasProd {
			continue
		}

		_, err := db.Exec(`
			INSERT INTO product_questions (product_id, user_id, question, answer, answered_by, answered_at, helpful_count, tenant_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, NOW() - INTERVAL '10 days', 8, $6, NOW() - INTERVAL '12 days', NOW())
			ON CONFLICT DO NOTHING;
		`, prodID, userID, q.Question, q.Answer, adminID, tenantID)
		if err != nil {
			log.Printf("Failed to seed product question for %s: %v", q.Slug, err)
		}
	}
}

// -------------------------------------------------------------
// ORDERS, ORDER ITEMS, PAYMENTS, SHIPMENTS
// -------------------------------------------------------------
func seedOrders(db *sql.DB, tenantID string, userIDs map[string]string, productIDs map[string]string, variantMap map[string][]VariantSeed) {
	fmt.Println("Seeding Realistic Order History (Past 30 Days)...")

	type OrderMock struct {
		OrderNumber    string
		Email          string
		ProductSlugs   []string
		Status         string // order_status enum
		PaymentStatus  string // payment_status enum
		ShipmentStatus string // shipment_status enum
		Courier        string
		TrackingNo     string
		Discount       int64
		ShippingCost   int64
		DaysAgo        int
		Address        map[string]any
	}

	mockOrders := []OrderMock{
		{
			OrderNumber:    "ORD-20260810-0012",
			Email:          "budi@example.com",
			ProductSlugs:   []string{"macbook-pro-14-m3", "logitech-mx-master-3s"},
			Status:         "DELIVERED",
			PaymentStatus:  "PAID",
			ShipmentStatus: "DELIVERED",
			Courier:        "JNE Express (YES)",
			TrackingNo:     "JNE882910394812",
			Discount:       100000,
			ShippingCost:   45000,
			DaysAgo:        24,
			Address: map[string]any{
				"receiver_name": "Budi Santoso",
				"phone":         "081234567890",
				"address_line":  "Jl. Sudirman No. 45, Senayan",
				"city":          "Jakarta Selatan",
				"postal_code":   "12190",
			},
		},
		{
			OrderNumber:    "ORD-20260815-0034",
			Email:          "siti@example.com",
			ProductSlugs:   []string{"sony-wh-1000xm5"},
			Status:         "DELIVERED",
			PaymentStatus:  "PAID",
			ShipmentStatus: "DELIVERED",
			Courier:        "SiCepat REG",
			TrackingNo:     "004128941029",
			Discount:       50000,
			ShippingCost:   22000,
			DaysAgo:        19,
			Address: map[string]any{
				"receiver_name": "Siti Aminah",
				"phone":         "081345678901",
				"address_line":  "Jl. Dago Asri No. 12",
				"city":          "Bandung",
				"postal_code":   "40135",
			},
		},
		{
			OrderNumber:    "ORD-20260820-0056",
			Email:          "andi@example.com",
			ProductSlugs:   []string{"nike-air-force-1-07", "erigo-sukajan-dragon"},
			Status:         "DELIVERED",
			PaymentStatus:  "PAID",
			ShipmentStatus: "DELIVERED",
			Courier:        "J&T Express",
			TrackingNo:     "JT9928103941",
			Discount:       25000,
			ShippingCost:   30000,
			DaysAgo:        14,
			Address: map[string]any{
				"receiver_name": "Andi Pratama",
				"phone":         "081456789012",
				"address_line":  "Jl. Pemuda No. 88, Gubeng",
				"city":          "Surabaya",
				"postal_code":   "60271",
			},
		},
		{
			OrderNumber:    "ORD-20260825-0088",
			Email:          "dewi@example.com",
			ProductSlugs:   []string{"somethinc-niacinamide-serum", "scarlett-whitening-body-lotion"},
			Status:         "DELIVERED",
			PaymentStatus:  "PAID",
			ShipmentStatus: "DELIVERED",
			Courier:        "Anteraja Reguler",
			TrackingNo:     "10002938491029",
			Discount:       20000,
			ShippingCost:   18000,
			DaysAgo:        9,
			Address: map[string]any{
				"receiver_name": "Dewi Lestari",
				"phone":         "081567890123",
				"address_line":  "Jl. Malioboro No. 23",
				"city":          "Yogyakarta",
				"postal_code":   "55271",
			},
		},
		{
			OrderNumber:    "ORD-20260829-0102",
			Email:          "eko@example.com",
			ProductSlugs:   []string{"iphone-15-pro"},
			Status:         "SHIPPED",
			PaymentStatus:  "PAID",
			ShipmentStatus: "IN_TRANSIT",
			Courier:        "JNE Express (YES)",
			TrackingNo:     "JNE883901928491",
			Discount:       100000,
			ShippingCost:   55000,
			DaysAgo:        5,
			Address: map[string]any{
				"receiver_name": "Eko Kurniawan",
				"phone":         "081678901234",
				"address_line":  "Jl. Gatot Subroto No. 5",
				"city":          "Medan",
				"postal_code":   "20114",
			},
		},
		{
			OrderNumber:    "ORD-20260901-0125",
			Email:          "fajar@example.com",
			ProductSlugs:   []string{"sony-ps5-slim"},
			Status:         "PACKED",
			PaymentStatus:  "PAID",
			ShipmentStatus: "PICKED",
			Courier:        "SiCepat GOKIL (Cargo)",
			TrackingNo:     "004291039401",
			Discount:       0,
			ShippingCost:   65000,
			DaysAgo:        2,
			Address: map[string]any{
				"receiver_name": "Fajar Nugraha",
				"phone":         "081789012345",
				"address_line":  "Jl. Sunset Road No. 101, Seminyak",
				"city":          "Badung (Bali)",
				"postal_code":   "80361",
			},
		},
		{
			OrderNumber:    "ORD-20260902-0140",
			Email:          "gita@example.com",
			ProductSlugs:   []string{"adidas-ultraboost-light"},
			Status:         "PAID",
			PaymentStatus:  "PAID",
			ShipmentStatus: "PENDING",
			Courier:        "J&T Express",
			TrackingNo:     "",
			Discount:       50000,
			ShippingCost:   24000,
			DaysAgo:        1,
			Address: map[string]any{
				"receiver_name": "Gita Pertiwi",
				"phone":         "081890123456",
				"address_line":  "Jl. Dhoho No. 77",
				"city":          "Kediri",
				"postal_code":   "64129",
			},
		},
		{
			OrderNumber:    "ORD-20260903-0168",
			Email:          "dimas@example.com",
			ProductSlugs:   []string{"eiger-diario-28l", "erigo-sukajan-dragon"},
			Status:         "PENDING_PAYMENT",
			PaymentStatus:  "PENDING",
			ShipmentStatus: "PENDING",
			Courier:        "JNE Reguler",
			TrackingNo:     "",
			Discount:       0,
			ShippingCost:   28000,
			DaysAgo:        0,
			Address: map[string]any{
				"receiver_name": "Dimas Arya",
				"phone":         "082445678901",
				"address_line":  "Jl. Diponegoro No. 34",
				"city":          "Semarang",
				"postal_code":   "50131",
			},
		},
		{
			OrderNumber:    "ORD-20260818-0045",
			Email:          "rina@example.com",
			ProductSlugs:   []string{"lego-star-wars-millennium-falcon"},
			Status:         "CANCELLED",
			PaymentStatus:  "EXPIRED",
			ShipmentStatus: "PENDING",
			Courier:        "J&T Express",
			TrackingNo:     "",
			Discount:       0,
			ShippingCost:   40000,
			DaysAgo:        16,
			Address: map[string]any{
				"receiver_name": "Rina Anggraini",
				"phone":         "082334567890",
				"address_line":  "Jl. Veteran No. 8",
				"city":          "Malang",
				"postal_code":   "65145",
			},
		},
	}

	for _, o := range mockOrders {
		userID, hasUser := userIDs[o.Email]
		if !hasUser {
			continue
		}

		// Calculate Totals
		var subtotal int64
		type itemCalc struct {
			ProdID    string
			VariantID sql.NullString
			Title     string
			Slug      string
			Price     int64
			Qty       int
		}
		var items []itemCalc

		for _, slug := range o.ProductSlugs {
			prodID, ok := productIDs[slug]
			if !ok {
				continue
			}
			variants := variantMap[slug]
			var price int64 = 100000
			var varID sql.NullString

			if len(variants) > 0 {
				price = variants[0].Price
				// get variant id from db
				var vid string
				if err := db.QueryRow("SELECT id FROM product_variants WHERE product_id = $1 LIMIT 1", prodID).Scan(&vid); err == nil {
					varID = sql.NullString{String: vid, Valid: true}
				}
			}

			// Get product title
			var title string
			_ = db.QueryRow("SELECT title FROM products WHERE id = $1", prodID).Scan(&title)

			qty := 1
			items = append(items, itemCalc{
				ProdID:    prodID,
				VariantID: varID,
				Title:     title,
				Slug:      slug,
				Price:     price,
				Qty:       qty,
			})
			subtotal += price * int64(qty)
		}

		total := subtotal - o.Discount + o.ShippingCost
		if total < 0 {
			total = 0
		}

		addrJSON, _ := json.Marshal(o.Address)
		shipOptJSON, _ := json.Marshal(map[string]any{"courier": o.Courier, "cost": o.ShippingCost})

		// Insert Order
		var orderID string
		orderCreatedAt := time.Now().Add(-time.Duration(o.DaysAgo*24) * time.Hour)
		err := db.QueryRow(`
			INSERT INTO orders (tenant_id, user_id, order_number, status, currency, pricing_subtotal, pricing_discount, pricing_shipping, pricing_total, shipping_address, shipping_option, notes, created_at, updated_at)
			VALUES ($1, $2, $3, $4::order_status, 'IDR', $5, $6, $7, $8, $9, $10, 'Tolong packing ekstra aman bubble wrap ya min', $11, $11)
			ON CONFLICT (order_number) WHERE order_number IS NOT NULL DO UPDATE SET
				status = EXCLUDED.status,
				pricing_total = EXCLUDED.pricing_total,
				updated_at = EXCLUDED.updated_at
			RETURNING id;
		`, tenantID, userID, o.OrderNumber, o.Status, subtotal, o.Discount, o.ShippingCost, total, addrJSON, shipOptJSON, orderCreatedAt).Scan(&orderID)

		if err != nil {
			log.Printf("Failed to insert order %s: %v", o.OrderNumber, err)
			continue
		}

		// Insert Order Items
		for _, it := range items {
			itemSub := it.Price * int64(it.Qty)
			_, _ = db.Exec(`
				INSERT INTO order_items (order_id, product_id, variant_id, title, slug, qty, unit_price, subtotal)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				ON CONFLICT DO NOTHING;
			`, orderID, it.ProdID, it.VariantID, it.Title, it.Slug, it.Qty, it.Price, itemSub)
		}

		// Insert Payment
		_, _ = db.Exec(`
			INSERT INTO payments (order_id, provider, status, created_at, updated_at)
			VALUES ($1, 'midtrans', $2::payment_status, $3, $3)
			ON CONFLICT DO NOTHING;
		`, orderID, o.PaymentStatus, orderCreatedAt)

		// Insert Shipment
		_, _ = db.Exec(`
			INSERT INTO shipments (order_id, status, courier, tracking_number)
			VALUES ($1, $2::shipment_status, $3, $4)
			ON CONFLICT DO NOTHING;
		`, orderID, o.ShipmentStatus, o.Courier, o.TrackingNo)
	}
}

// -------------------------------------------------------------
// SUPPORT TICKETS
// -------------------------------------------------------------
func seedSupportTickets(db *sql.DB, tenantID string, userIDs map[string]string) {
	fmt.Println("Seeding Support Tickets & Messages...")

	adminID := userIDs["admin@toko.com"]
	tickets := []struct {
		Email    string
		Subject  string
		Status   string
		Priority string
		Messages []struct {
			IsAdmin bool
			Body    string
		}
	}{
		{
			Email:    "budi@example.com",
			Subject:  "Konfirmasi Klaim Garansi Apple MacBook Pro",
			Status:   "RESOLVED",
			Priority: "NORMAL",
			Messages: []struct {
				IsAdmin bool
				Body    string
			}{
				{false, "Halo admin Toko Kios, saya ingin menanyakan apakah invoice digital dari aplikasi ini sudah cukup untuk klaim garansi di Apple Service Center resmi?"},
				{true, "Halo Kak Budi, betul sekali. Anda cukup menunjukkan bukti invoice digital Toko Kios dan nomor seri MacBook Pro yang tertera. Seluruh mitra resmi Apple Indonesia akan menerima klaim garansi Anda."},
				{false, "Baik, terima kasih banyak atas informasinya min!"},
			},
		},
		{
			Email:    "eko@example.com",
			Subject:  "Update Resi Pengiriman iPhone 15 Pro",
			Status:   "OPEN",
			Priority: "HIGH",
			Messages: []struct {
				IsAdmin bool
				Body    string
			}{
				{false, "Min, pesanan saya ORD-20260829-0102 sudah dikirim via JNE YES tapi status trackingnya belum update di website JNE, mohon bantu cek ya."},
				{true, "Halo Kak Eko, pesanan kakak sudah diserahkan ke kurir JNE sore tadi. Biasanya update tracking membutuhkan waktu 1-3 jam setelah barcode di-scan di hub transit. Kami terus monitor ya Kak."},
			},
		},
		{
			Email:    "siti@example.com",
			Subject:  "Pertanyaan Penggunaan Voucher Diskon",
			Status:   "CLOSED",
			Priority: "LOW",
			Messages: []struct {
				IsAdmin bool
				Body    string
			}{
				{false, "Apakah voucher WELCOME50K bisa digabung dengan promo gratis ongkir?"},
				{true, "Halo Kak Siti, sistem saat ini mengizinkan penggunaan 1 voucher per transaksi checkout. Terima kasih atas pengertiannya Kak."},
			},
		},
	}

	for _, t := range tickets {
		userID, ok := userIDs[t.Email]
		if !ok {
			continue
		}

		var ticketID string
		err := db.QueryRow(`
			INSERT INTO support_tickets (tenant_id, user_id, subject, status, priority, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, NOW() - INTERVAL '5 days', NOW())
			RETURNING id;
		`, tenantID, userID, t.Subject, t.Status, t.Priority).Scan(&ticketID)

		if err != nil {
			log.Printf("Failed to insert ticket: %v", err)
			continue
		}

		for idx, msg := range t.Messages {
			authorID := userID
			if msg.IsAdmin {
				authorID = adminID
			}
			msgTime := time.Now().Add(-time.Duration((5*24)-(idx*2)) * time.Hour)
			_, _ = db.Exec(`
				INSERT INTO support_messages (ticket_id, author_id, body, created_at)
				VALUES ($1, $2, $3, $4);
			`, ticketID, authorID, msg.Body, msgTime)
		}
	}
}

// -------------------------------------------------------------
// LOYALTY & REWARDS
// -------------------------------------------------------------
func seedLoyalty(db *sql.DB, userIDs map[string]string) {
	fmt.Println("Seeding Loyalty Profiles & Rewards...")

	// 1. Rewards Catalog
	rewards := []struct {
		Name        string
		Description string
		Points      int
	}{
		{"Voucher Belanja Rp 25.000", "Potongan langsung belanja Rp 25.000 tanpa minimal pembelian.", 250},
		{"Voucher Belanja Rp 50.000", "Potongan langsung belanja Rp 50.000 untuk semua kategori produk.", 450},
		{"Gratis Ongkir Khusus Member", "Bebas ongkir pengiriman ke seluruh pulau Jawa hingga Rp 30.000.", 150},
		{"Exclusive Toko Kios Tote Bag", "Merchandise eksklusif premium canvas tote bag.", 600},
	}

	for _, r := range rewards {
		_, _ = db.Exec(`
			INSERT INTO loyalty_rewards (name, description, points_cost, active, created_at, updated_at)
			VALUES ($1, $2, $3, true, NOW() - INTERVAL '30 days', NOW())
			ON CONFLICT DO NOTHING;
		`, r.Name, r.Description, r.Points)
	}

	// 2. Member Profiles & Points
	profiles := []struct {
		Email    string
		Points   int
		Tier     string
		Lifetime int
	}{
		{"budi@example.com", 850, "gold", 2500},
		{"siti@example.com", 420, "silver", 1200},
		{"andi@example.com", 260, "silver", 800},
		{"dewi@example.com", 150, "bronze", 300},
		{"eko@example.com", 950, "gold", 3200},
	}

	for _, p := range profiles {
		userID, ok := userIDs[p.Email]
		if !ok {
			continue
		}

		_, err := db.Exec(`
			INSERT INTO loyalty_profiles (user_id, points, tier, tier_progress, lifetime_points, joined_at)
			VALUES ($1, $2, $3, 75, $4, NOW() - INTERVAL '60 days')
			ON CONFLICT (user_id) DO UPDATE SET
				points = EXCLUDED.points,
				tier = EXCLUDED.tier,
				lifetime_points = EXCLUDED.lifetime_points;
		`, userID, p.Points, p.Tier, p.Lifetime)

		if err != nil {
			log.Printf("Failed to seed loyalty profile for %s: %v", p.Email, err)
			continue
		}

		// Add sample points transactions
		_, _ = db.Exec(`
			INSERT INTO loyalty_transactions (user_id, type, points, balance, description, reference_type, created_at)
			VALUES
				($1, 'earned', 500, 500, 'Poin transaksi belanja selesai', 'order', NOW() - INTERVAL '20 days'),
				($1, 'earned', 100, 600, 'Bonus ulasan produk dan foto', 'review', NOW() - INTERVAL '15 days'),
				($1, 'bonus', 250, $2, 'Bonus ulang tahun member', 'promo', NOW() - INTERVAL '5 days');
		`, userID, p.Points)
	}
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
