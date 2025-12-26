package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	// Load .env file
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
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping DB: %v", err)
	}

	// Get Default Tenant
	var tenantID string
	err = db.QueryRow("SELECT id FROM tenants WHERE slug = 'default'").Scan(&tenantID)
	if err != nil {
		// If default tenant missing, try to insert it (backfill logic similar to migration)
		log.Println("Default tenant not found, attempting to create...")
		err = db.QueryRow(`
			INSERT INTO tenants (name, slug) VALUES ('Default Tenant', 'default')
			ON CONFLICT (slug) DO UPDATE SET name=EXCLUDED.name
			RETURNING id;
		`).Scan(&tenantID)
		if err != nil {
			log.Fatalf("Failed to retrieve or create default tenant: %v", err)
		}
	}
	log.Printf("Using Tenant ID: %s", tenantID)

	seedUsers(db)
	seedCatalog(db, tenantID)
	seedVouchers(db, tenantID)
	seedAddresses(db)
	
	log.Println("Seeding completed successfully!")
}

func seedUsers(db *sql.DB) {
	users := []struct {
		Name  string
		Email string
		Role  string
	}{
		{"Admin User", "admin@toko.com", "admin"},
		{"Noah Developer", "noah@toko.com", "admin"},
		{"Budi Santoso", "budi@example.com", "customer"},
		{"Siti Aminah", "siti@example.com", "customer"},
		{"Andi Pratama", "andi@example.com", "customer"},
		{"Dewi Lestari", "dewi@example.com", "customer"},
		{"Eko Kurniawan", "eko@example.com", "customer"},
		{"Fajar Nugraha", "fajar@example.com", "customer"},
		{"Gita Pertiwi", "gita@example.com", "customer"},
		{"Hendra Wijaya", "hendra@example.com", "customer"},
		{"Indah Sari", "indah@example.com", "customer"},
		{"Joko Widodo", "joko@example.com", "customer"},
	}

	fmt.Println("Seeding Users...")
	for _, u := range users {
		_, err := db.Exec(`
			INSERT INTO users (name, email, password_hash, roles)
			VALUES ($1, $2, crypt('password123', gen_salt('bf')), ARRAY[$3])
			ON CONFLICT (email) DO NOTHING;
		`, u.Name, u.Email, u.Role)
		if err != nil {
			log.Printf("Failed to seed user %s: %v", u.Email, err)
		}
	}
}

func seedCatalog(db *sql.DB, tenantID string) {
	// 1. Categories
	categories := []struct {
		Name string
		Slug string
	}{
		{"Electronics", "electronics"},
		{"Fashion", "fashion"},
		{"Home & Living", "home-living"},
		{"Beauty", "beauty"},
		{"Sports", "sports"},
		{"Toys", "toys"},
		{"Books", "books"},
		{"Automotive", "automotive"},
		{"Health", "health"},
		{"Garden", "garden"},
	}

	fmt.Println("Seeding Categories...")
	catIDs := make(map[string]string)
	for _, c := range categories {
		_, err := db.Exec(`
			INSERT INTO categories (name, slug, tenant_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name;
		`, c.Name, c.Slug, tenantID)
		if err != nil {
			log.Printf("Failed to upsert category %s: %v", c.Name, err)
		}

		var id string
		if err := db.QueryRow("SELECT id FROM categories WHERE slug = $1", c.Slug).Scan(&id); err != nil {
			log.Printf("Failed to get ID for category %s: %v", c.Name, err)
			continue
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
		{"Nike", "nike"},
		{"Adidas", "adidas"},
		{"Ikea", "ikea"},
		{"Dyson", "dyson"},
		{"Lego", "lego"},
		{"Sony", "sony"},
		{"Dell", "dell"},
		{"Canon", "canon"},
	}

	fmt.Println("Seeding Brands...")
	brandIDs := make(map[string]string)
	for _, b := range brands {
		_, err := db.Exec(`
			INSERT INTO brands (name, slug, tenant_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name;
		`, b.Name, b.Slug, tenantID)
		if err != nil {
			log.Printf("Failed to upsert brand %s: %v", b.Name, err)
		}

		var id string
		if err := db.QueryRow("SELECT id FROM brands WHERE slug = $1", b.Slug).Scan(&id); err != nil {
			log.Printf("Failed to get ID for brand %s: %v", b.Name, err)
			continue
		}
		brandIDs[b.Slug] = id
	}

	// 3. Products
	products := []struct {
		Title    string
		Slug     string
		Brand    string
		Category string
		Price    int64
		Image    string
		Stock    int
	}{
		{"MacBook Pro 14 M3", "macbook-pro-14-m3", "apple", "electronics", 25000000, "https://images.unsplash.com/photo-1517336714731-489689fd1ca8?w=800", 50},
		{"iPhone 15 Pro", "iphone-15-pro", "apple", "electronics", 20000000, "https://images.unsplash.com/photo-1695048133142-1a20484d2569?w=800", 100},
		{"Samsung Galaxy S24 Ultra", "samsung-galaxy-s24", "samsung", "electronics", 19000000, "https://images.unsplash.com/photo-1610945415295-d9bbf067e59c?w=800", 80},
		{"Sony WH-1000XM5", "sony-wh-1000xm5", "sony", "electronics", 5000000, "https://images.unsplash.com/photo-1618366712010-f4ae9c647dcb?w=800", 150},
		{"Dell XPS 13", "dell-xps-13", "dell", "electronics", 18000000, "https://images.unsplash.com/photo-1593642632823-8f785667771b?w=800", 40},
		{"Canon EOS R5", "canon-eos-r5", "canon", "electronics", 45000000, "https://images.unsplash.com/photo-1516035069371-29a1b244cc32?w=800", 20},
		{"Nike Air Force 1", "nike-air-force-1", "nike", "fashion", 1500000, "https://images.unsplash.com/photo-1542291026-7eec264c27ff?w=800", 200},
		{"Adidas Ultraboost", "adidas-ultraboost", "adidas", "fashion", 2000000, "https://images.unsplash.com/photo-1608231387042-66d1773070a5?w=800", 180},
		{"IKEAS LANDSKRONA Sofa", "ikea-landskrona", "ikea", "home-living", 8000000, "https://images.unsplash.com/photo-1555041469-a586c61ea9bc?w=800", 10},
		{"Dyson V15 Detect", "dyson-v15", "dyson", "home-living", 12000000, "https://images.unsplash.com/photo-1556911220-e15b29be8c8f?w=800", 30},
		{"LEGO Star Wars Millenium Falcon", "lego-millenium-falcon", "lego", "toys", 13000000, "https://images.unsplash.com/photo-1585366119957-e9730b6d0f60?w=800", 25},
		{"Sony PlayStation 5", "sony-ps5", "sony", "electronics", 9000000, "https://images.unsplash.com/photo-1606813907291-d86efa9b94db?w=800", 60},
		{"Kaos Hitam Polos", "kaos-hitam-polos", "nike", "fashion", 100000, "https://images.unsplash.com/photo-1583743814966-8936f5b7be1a?w=800", 500},
	}

	fmt.Println("Seeding Products...")
	for _, p := range products {
		catID, ok1 := catIDs[p.Category]
		brandID, ok2 := brandIDs[p.Brand]
		
		if !ok1 {
			log.Printf("Missing category ID for %s", p.Category)
		}
		if !ok2 {
			log.Printf("Missing brand ID for %s", p.Brand)
		}
		
		if !ok1 || !ok2 {
			continue
		}

		// Insert Product
		var prodID string
		err := db.QueryRow(`
			INSERT INTO products (title, slug, brand_id, category_id, price, in_stock, thumbnail, tenant_id)
			VALUES ($1, $2, $3, $4, $5, true, $6, $7)
			ON CONFLICT (slug) DO UPDATE SET 
				price = EXCLUDED.price,
				brand_id = EXCLUDED.brand_id,
				category_id = EXCLUDED.category_id,
				thumbnail = EXCLUDED.thumbnail,
				tenant_id = EXCLUDED.tenant_id
			RETURNING id;
		`, p.Title, p.Slug, brandID, catID, p.Price, p.Image, tenantID).Scan(&prodID)

		if err != nil {
			log.Printf("Failed to seed product %s: %v", p.Title, err)
			continue
		}

		// Insert/Update Product Variant
		sku := strings.ToUpper(strings.ReplaceAll(p.Slug, "-", ""))
		_, err = db.Exec(`
			INSERT INTO product_variants (product_id, sku, price, stock)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (sku) DO UPDATE SET 
				stock = EXCLUDED.stock,
				price = EXCLUDED.price;
		`, prodID, sku, p.Price, p.Stock)
		
		if err != nil {
			log.Printf("Failed to seed variant for %s: %v", p.Title, err)
		}
	}
}

func seedVouchers(db *sql.DB, tenantID string) {
	vouchers := []struct {
		Code   string
		Value  int64
		Kind   string // "percent" or "fixed_amount"
	}{
		{"DISC20", 20000, "fixed_amount"},
		{"WELCOME", 50000, "fixed_amount"},
	}

	fmt.Println("Seeding Vouchers...")
	for _, v := range vouchers {
		_, err := db.Exec(`
			INSERT INTO vouchers (code, value, kind, tenant_id, valid_from, valid_to, min_spend)
			VALUES ($1, $2, $3, $4, NOW(), NOW() + INTERVAL '1 year', 0)
			ON CONFLICT (code) DO NOTHING;
		`, v.Code, v.Value, v.Kind, tenantID)
		if err != nil {
			log.Printf("Failed to seed voucher %s: %v", v.Code, err)
		}
	}
}

func seedAddresses(db *sql.DB) {
	// Seed address for 'noah@toko.com' (Admin) or 'budi@example.com' (Customer)
	var userID string
	err := db.QueryRow("SELECT id FROM users WHERE email = 'budi@example.com'").Scan(&userID)
	if err != nil {
		log.Printf("Skipping address seed: user 'budi@example.com' not found: %v", err)
		return
	}

	fmt.Println("Seeding Addresses...")
	_, err = db.Exec(`
		INSERT INTO addresses (user_id, receiver_name, phone, address_line1, city, postal_code, is_default)
		VALUES ($1, 'Budi Santoso', '08123456789', 'Jl. Sudirman No. 1', 'Jakarta Selatan', '12190', true)
		ON CONFLICT DO NOTHING;
	`, userID)
	if err != nil {
		log.Printf("Failed to seed address: %v", err)
	}
}
