# Product catalog seed data for migration 000025_full_seed_catalog.
#
# Kept as Python (not raw SQL) so the SQL stays generated and consistent:
# prices, slugs, variant SKUs and image ordering all follow one set of rules.
# Regenerate with: python3 scripts/seed/gen_seed_sql.py

CATEGORIES = [
    ("Electronics", "electronics"),
    ("Fashion", "fashion"),
    ("Home & Living", "home-living"),
    ("Beauty", "beauty"),
    ("Sports", "sports"),
    ("Toys", "toys"),
    ("Books", "books"),
    ("Automotive", "automotive"),
    ("Health", "health"),
    ("Garden", "garden"),
]

BRANDS = [
    ("Apple", "apple"),
    ("Samsung", "samsung"),
    ("Sony", "sony"),
    ("Dell", "dell"),
    ("Canon", "canon"),
    ("Nike", "nike"),
    ("Adidas", "adidas"),
    ("Uniqlo", "uniqlo"),
    ("IKEA", "ikea"),
    ("Dyson", "dyson"),
    ("Philips", "philips"),
    ("LEGO", "lego"),
    ("The Body Shop", "the-body-shop"),
    ("Wardah", "wardah"),
    ("Gramedia", "gramedia"),
    ("Shimano", "shimano"),
    ("Bosch", "bosch"),
    ("Xiaomi", "xiaomi"),
    ("Acme", "acme"),
]

# Each product: slug, title, brand, category, price (IDR), compare_at or None,
# in_stock, badges, description, specs (list of key/value), variants
# (list of (sku_suffix, price_delta, stock, attributes dict)), image_query.
PRODUCTS = [
    # --- Electronics -------------------------------------------------------
    dict(
        slug="macbook-pro-14-m3", title="MacBook Pro 14 M3", brand="apple",
        category="electronics", price=25_000_000, compare_at=27_500_000,
        badges=["new", "bestseller"],
        description=(
            "Laptop MacBook Pro 14 inci dengan chip Apple M3, layar Liquid Retina "
            "XDR, dan daya tahan baterai hingga 18 jam. Cocok untuk editing video, "
            "kompilasi kode, dan pekerjaan kreatif berat."
        ),
        specs=[("Prosesor", "Apple M3 8-core"), ("Layar", "14.2\" Liquid Retina XDR 120Hz"),
               ("Baterai", "70Wh, hingga 18 jam"), ("Port", "2x Thunderbolt 4, HDMI, SDXC"),
               ("Berat", "1.55 kg"), ("Garansi", "12 bulan resmi")],
        variants=[("8-512-SLV", 0, 12, {"storage": "512GB", "color": "Silver"}),
                  ("8-1TB-SLV", 3_000_000, 7, {"storage": "1TB", "color": "Silver"}),
                  ("8-1TB-BLK", 3_200_000, 5, {"storage": "1TB", "color": "Space Black"})],
        image_query="macbook pro laptop on desk",
    ),
    dict(
        slug="iphone-15-pro", title="iPhone 15 Pro", brand="apple",
        category="electronics", price=20_000_000, compare_at=21_999_000,
        badges=["bestseller"],
        description=(
            "Handphone iPhone 15 Pro dengan bodi titanium, chip A17 Pro, dan sistem "
            "kamera 48MP. Action Button baru dan port USB-C dengan transfer cepat."
        ),
        specs=[("Chip", "A17 Pro"), ("Layar", "6.1\" Super Retina XDR ProMotion"),
               ("Kamera", "48MP utama + 12MP ultrawide + 12MP telephoto"),
               ("Material", "Titanium"), ("Konektor", "USB-C"), ("Garansi", "12 bulan resmi")],
        variants=[("128-NTI", 0, 20, {"storage": "128GB", "color": "Natural Titanium"}),
                  ("256-NTI", 2_000_000, 15, {"storage": "256GB", "color": "Natural Titanium"}),
                  ("256-BTI", 2_000_000, 9, {"storage": "256GB", "color": "Blue Titanium"})],
        image_query="iphone 15 pro smartphone",
    ),
    dict(
        slug="samsung-galaxy-s24-ultra", title="Samsung Galaxy S24 Ultra", brand="samsung",
        category="electronics", price=19_000_000, compare_at=22_000_000,
        badges=["promo"],
        description=(
            "Handphone Galaxy S24 Ultra dengan S Pen terintegrasi, kamera 200MP, "
            "dan layar Dynamic AMOLED 2X 6.8 inci anti-refleksi."
        ),
        specs=[("Chip", "Snapdragon 8 Gen 3 for Galaxy"), ("Layar", "6.8\" Dynamic AMOLED 2X 120Hz"),
               ("Kamera", "200MP + 50MP + 12MP + 10MP"), ("Baterai", "5000mAh 45W"),
               ("S Pen", "Termasuk"), ("Garansi", "12 bulan resmi SEIN")],
        variants=[("256-GRY", 0, 18, {"storage": "256GB", "color": "Titanium Gray"}),
                  ("512-GRY", 2_500_000, 10, {"storage": "512GB", "color": "Titanium Gray"}),
                  ("512-VIO", 2_500_000, 6, {"storage": "512GB", "color": "Titanium Violet"})],
        image_query="samsung galaxy s24 ultra phone",
    ),
    dict(
        slug="sony-wh-1000xm5", title="Sony WH-1000XM5", brand="sony",
        category="electronics", price=5_000_000, compare_at=5_999_000,
        badges=["promo", "bestseller"],
        description=(
            "Headphone over-ear dengan noise cancelling adaptif, 8 mikrofon, dan "
            "baterai 30 jam. Mode percakapan otomatis saat Anda mulai berbicara."
        ),
        specs=[("Tipe", "Over-ear wireless"), ("ANC", "Dual processor, 8 mic"),
               ("Baterai", "30 jam ANC aktif"), ("Codec", "LDAC, AAC, SBC"),
               ("Berat", "250 g"), ("Garansi", "12 bulan resmi")],
        variants=[("BLK", 0, 25, {"color": "Black"}),
                  ("SLV", 0, 18, {"color": "Platinum Silver"})],
        image_query="sony wireless headphones black",
    ),
    dict(
        slug="dell-xps-13-plus", title="Dell XPS 13 Plus", brand="dell",
        category="electronics", price=18_000_000, compare_at=None,
        badges=[],
        description=(
            "Laptop ultrabook 13 inci dengan Intel Core Ultra 7, keyboard "
            "edge-to-edge, dan bodi aluminium CNC. Ringan untuk mobilitas harian."
        ),
        specs=[("Prosesor", "Intel Core Ultra 7 155H"), ("RAM", "16GB LPDDR5"),
               ("Layar", "13.4\" FHD+ InfinityEdge"), ("Penyimpanan", "512GB NVMe SSD"),
               ("Berat", "1.24 kg"), ("Garansi", "12 bulan")],
        variants=[("16-512", 0, 8, {"ram": "16GB", "storage": "512GB"}),
                  ("32-1TB", 5_000_000, 4, {"ram": "32GB", "storage": "1TB"})],
        image_query="dell xps ultrabook laptop",
    ),
    dict(
        slug="canon-eos-r5", title="Canon EOS R5", brand="canon",
        category="electronics", price=45_000_000, compare_at=48_000_000,
        badges=["premium"],
        description=(
            "Kamera mirrorless full-frame 45MP dengan video 8K RAW, IBIS 8 stop, "
            "dan autofokus Dual Pixel CMOS AF II."
        ),
        specs=[("Sensor", "45MP full-frame CMOS"), ("Video", "8K RAW 30p, 4K 120p"),
               ("Stabilisasi", "IBIS hingga 8 stop"), ("Mount", "Canon RF"),
               ("Slot Kartu", "CFexpress + SD UHS-II"), ("Garansi", "12 bulan resmi Datascrip")],
        variants=[("BODY", 0, 5, {"kit": "Body only"}),
                  ("KIT-24105", 12_000_000, 3, {"kit": "RF 24-105mm f/4L"})],
        image_query="canon mirrorless camera",
    ),
    dict(
        slug="sony-playstation-5-slim", title="Sony PlayStation 5 Slim", brand="sony",
        category="electronics", price=9_000_000, compare_at=9_799_000,
        badges=["bestseller"],
        description=(
            "Konsol PS5 versi Slim dengan SSD ultra cepat, ray tracing, dan "
            "controller DualSense haptic feedback."
        ),
        specs=[("CPU", "AMD Zen 2 8-core"), ("GPU", "AMD RDNA 2, 10.28 TFLOPS"),
               ("Penyimpanan", "1TB SSD custom"), ("Output", "4K 120Hz, 8K siap"),
               ("Controller", "DualSense"), ("Garansi", "12 bulan resmi")],
        variants=[("DISC", 0, 14, {"edition": "Disc"}),
                  ("DIGITAL", -1_000_000, 11, {"edition": "Digital"})],
        image_query="playstation 5 console",
    ),
    dict(
        slug="xiaomi-redmi-note-13-pro", title="Xiaomi Redmi Note 13 Pro", brand="xiaomi",
        category="electronics", price=3_499_000, compare_at=3_999_000,
        badges=["promo"],
        description=(
            "Handphone smartphone mid-range dengan kamera 200MP OIS, layar "
            "AMOLED 120Hz, dan pengisian cepat 67W."
        ),
        specs=[("Chip", "MediaTek Helio G99 Ultra"), ("Layar", "6.67\" AMOLED 120Hz"),
               ("Kamera", "200MP OIS + 8MP + 2MP"), ("Baterai", "5100mAh 67W"),
               ("Sistem", "HyperOS"), ("Garansi", "15 bulan resmi")],
        variants=[("8-256-BLK", 0, 30, {"ram": "8GB", "storage": "256GB", "color": "Midnight Black"}),
                  ("12-512-BLU", 900_000, 16, {"ram": "12GB", "storage": "512GB", "color": "Ocean Teal"})],
        image_query="xiaomi redmi smartphone",
    ),
    dict(
        slug="samsung-smart-tv-55-crystal-uhd", title="Samsung Smart TV 55\" Crystal UHD",
        brand="samsung", category="electronics", price=7_499_000, compare_at=8_999_000,
        badges=["promo"],
        description=(
            "Smart TV 55 inci 4K Crystal Processor dengan Tizen OS, HDR10+, dan "
            "Object Tracking Sound Lite."
        ),
        specs=[("Ukuran", "55 inci"), ("Resolusi", "3840x2160 (4K UHD)"),
               ("Prosesor", "Crystal Processor 4K"), ("HDR", "HDR10+"),
               ("Sistem", "Tizen Smart TV"), ("Garansi", "24 bulan panel")],
        variants=[("55", 0, 9, {"size": "55 inch"}),
                  ("65", 3_500_000, 5, {"size": "65 inch"})],
        image_query="samsung smart tv living room",
    ),
    dict(
        slug="apple-ipad-air-11-m2", title="Apple iPad Air 11 M2", brand="apple",
        category="electronics", price=10_999_000, compare_at=None,
        badges=["new"],
        description=(
            "Tablet iPad Air 11 inci dengan chip M2, dukungan Apple Pencil Pro, "
            "dan layar Liquid Retina laminated."
        ),
        specs=[("Chip", "Apple M2"), ("Layar", "11\" Liquid Retina"),
               ("Kamera", "12MP wide, 12MP ultrawide front"),
               ("Konektor", "USB-C"), ("Aksesori", "Apple Pencil Pro, Magic Keyboard"),
               ("Garansi", "12 bulan resmi")],
        variants=[("128-WIFI", 0, 13, {"storage": "128GB", "connectivity": "Wi-Fi"}),
                  ("256-WIFI", 2_000_000, 8, {"storage": "256GB", "connectivity": "Wi-Fi"}),
                  ("256-CELL", 4_500_000, 4, {"storage": "256GB", "connectivity": "Wi-Fi + Cellular"})],
        image_query="ipad tablet on table",
    ),
    dict(
        slug="philips-air-fryer-xl", title="Philips Air Fryer XL 6.2L", brand="philips",
        category="electronics", price=2_299_000, compare_at=2_799_000,
        badges=["promo", "bestseller"],
        description=(
            "Air fryer kapasitas 6.2 liter dengan teknologi Rapid Air, 7 preset "
            "memasak, dan keranjang anti lengket yang aman dicuci mesin."
        ),
        specs=[("Kapasitas", "6.2 L"), ("Daya", "2000 W"),
               ("Preset", "7 program otomatis"), ("Suhu", "40-200 derajat C"),
               ("Pembersihan", "Keranjang dishwasher safe"), ("Garansi", "24 bulan")],
        variants=[("BLK", 0, 22, {"color": "Black"})],
        image_query="air fryer kitchen appliance",
    ),

    # --- Fashion -----------------------------------------------------------
    dict(
        slug="kaos-hitam", title="Kaos Hitam", brand="acme",
        category="fashion", price=249_000, compare_at=299_000,
        badges=["promo", "new"],
        description=(
            "Kaos hitam polos lengan pendek, bahan katun 100% combed 30s. "
            "Nyaman dipakai sehari-hari, jahitan rapi, dan tidak mudah melar. "
            "Tersedia berbagai ukuran dari S hingga XXL."
        ),
        specs=[("Bahan", "Katun Combed 30s"), ("Ukuran", "S, M, L, XL, XXL"),
               ("Warna", "Hitam Polos"), ("Jahitan", "Double stitch"),
               ("Perawatan", "Cuci dengan air dingin"), ("Garansi", "7 hari retur")],
        variants=[("S", 0, 50, {"size": "S"}),
                  ("M", 0, 100, {"size": "M"}),
                  ("L", 0, 120, {"size": "L"}),
                  ("XL", 0, 80, {"size": "XL"}),
                  ("XXL", 10_000, 40, {"size": "XXL"})],
        image_query="black t shirt folded",
    ),
    dict(
        slug="nike-air-force-1-low", title="Nike Air Force 1 Low", brand="nike",
        category="fashion", price=1_499_000, compare_at=1_799_000,
        badges=["bestseller"],
        description=(
            "Sepatu sneaker ikonik dengan upper kulit, unit Nike Air di midsole, "
            "dan outsole karet non-marking. Siluet klasik yang cocok untuk harian."
        ),
        specs=[("Upper", "Kulit asli"), ("Midsole", "Nike Air cushioning"),
               ("Outsole", "Karet non-marking"), ("Tutup", "Lace-up"),
               ("Asal", "Vietnam"), ("Garansi", "7 hari tukar ukuran")],
        variants=[("40-WHT", 0, 14, {"size": "40", "color": "White"}),
                  ("41-WHT", 0, 18, {"size": "41", "color": "White"}),
                  ("42-WHT", 0, 21, {"size": "42", "color": "White"}),
                  ("42-BLK", 0, 12, {"size": "42", "color": "Black"}),
                  ("43-BLK", 0, 9, {"size": "43", "color": "Black"})],
        image_query="nike air force 1 sneakers",
    ),
    dict(
        slug="adidas-ultraboost-light", title="Adidas Ultraboost Light", brand="adidas",
        category="fashion", price=2_099_000, compare_at=2_600_000,
        badges=["promo"],
        description=(
            "Sepatu lari dengan busa Light BOOST 30 persen lebih ringan, upper "
            "Primeknit+, dan outsole Continental Rubber untuk cengkeraman basah."
        ),
        specs=[("Midsole", "Light BOOST"), ("Upper", "Primeknit+ rajut"),
               ("Outsole", "Continental Rubber"), ("Drop", "10 mm"),
               ("Berat", "299 g (UK 8.5)"), ("Penggunaan", "Lari harian")],
        variants=[("40-CBK", 0, 10, {"size": "40", "color": "Core Black"}),
                  ("41-CBK", 0, 13, {"size": "41", "color": "Core Black"}),
                  ("42-CWH", 0, 11, {"size": "42", "color": "Cloud White"}),
                  ("43-CWH", 0, 7, {"size": "43", "color": "Cloud White"})],
        image_query="adidas ultraboost running shoes",
    ),
    dict(
        slug="uniqlo-airism-cotton-tshirt", title="Uniqlo AIRism Cotton T-Shirt", brand="uniqlo",
        category="fashion", price=199_000, compare_at=249_000,
        badges=["promo"],
        description=(
            "Kaos lengan pendek dengan teknologi AIRism di sisi dalam sehingga "
            "menyerap keringat dan cepat kering, tetapi tetap terasa katun."
        ),
        specs=[("Material", "60% Katun, 40% Polyester AIRism"),
               ("Potongan", "Regular fit"), ("Kerah", "Crew neck"),
               ("Perawatan", "Cuci mesin suhu dingin"), ("Asal", "Bangladesh"),
               ("Musim", "All season")],
        variants=[("S-WHT", 0, 40, {"size": "S", "color": "White"}),
                  ("M-WHT", 0, 55, {"size": "M", "color": "White"}),
                  ("L-WHT", 0, 48, {"size": "L", "color": "White"}),
                  ("M-NVY", 0, 33, {"size": "M", "color": "Navy"}),
                  ("L-NVY", 0, 29, {"size": "L", "color": "Navy"}),
                  ("XL-BLK", 0, 22, {"size": "XL", "color": "Black"})],
        image_query="plain white t-shirt folded",
    ),
    dict(
        slug="uniqlo-selvedge-slim-jeans", title="Uniqlo Selvedge Slim Fit Jeans", brand="uniqlo",
        category="fashion", price=599_000, compare_at=799_000,
        badges=["promo"],
        description=(
            "Celana jeans selvedge denim 13.5 oz dengan potongan slim fit dan "
            "sedikit stretch untuk kenyamanan bergerak sepanjang hari."
        ),
        specs=[("Material", "98% Katun, 2% Elastane"), ("Berat Denim", "13.5 oz"),
               ("Potongan", "Slim fit"), ("Rise", "Mid rise"),
               ("Kaki", "Tapered"), ("Perawatan", "Cuci terbalik")],
        variants=[("30-IND", 0, 15, {"size": "30", "color": "Indigo"}),
                  ("32-IND", 0, 20, {"size": "32", "color": "Indigo"}),
                  ("34-IND", 0, 17, {"size": "34", "color": "Indigo"}),
                  ("32-BLK", 0, 12, {"size": "32", "color": "Black"})],
        image_query="folded blue denim jeans",
    ),
    dict(
        slug="nike-dri-fit-training-shorts", title="Nike Dri-FIT Training Shorts", brand="nike",
        category="fashion", price=399_000, compare_at=None,
        badges=[],
        description=(
            "Celana pendek latihan dengan teknologi Dri-FIT, saku samping "
            "berzipper, dan panel mesh di belakang untuk sirkulasi udara."
        ),
        specs=[("Teknologi", "Dri-FIT moisture wicking"), ("Panjang", "7 inci"),
               ("Saku", "2 saku samping zipper"), ("Pinggang", "Elastic drawcord"),
               ("Material", "100% Polyester recycled"), ("Perawatan", "Cuci mesin")],
        variants=[("S-BLK", 0, 25, {"size": "S", "color": "Black"}),
                  ("M-BLK", 0, 30, {"size": "M", "color": "Black"}),
                  ("L-GRY", 0, 19, {"size": "L", "color": "Grey"})],
        image_query="athletic training shorts",
    ),
    dict(
        slug="adidas-originals-trefoil-hoodie", title="Adidas Originals Trefoil Hoodie",
        brand="adidas", category="fashion", price=899_000, compare_at=1_100_000,
        badges=["promo"],
        description=(
            "Jaket hoodie fleece dengan logo Trefoil bordir, kantung kanguru, dan "
            "kupluk berlapis dua untuk kehangatan ekstra."
        ),
        specs=[("Material", "70% Katun, 30% Polyester fleece"),
               ("Potongan", "Regular fit"), ("Logo", "Trefoil bordir"),
               ("Saku", "Kanguru depan"), ("Kupluk", "Double layer"),
               ("Perawatan", "Cuci mesin suhu dingin")],
        variants=[("M-BLK", 0, 16, {"size": "M", "color": "Black"}),
                  ("L-BLK", 0, 14, {"size": "L", "color": "Black"}),
                  ("L-GRY", 0, 10, {"size": "L", "color": "Medium Grey Heather"}),
                  ("XL-GRY", 0, 8, {"size": "XL", "color": "Medium Grey Heather"})],
        image_query="grey hoodie sweatshirt",
    ),
    dict(
        slug="leather-minimalist-wallet", title="Dompet Kulit Minimalis RFID", brand="uniqlo",
        category="fashion", price=249_000, compare_at=349_000,
        badges=["promo"],
        description=(
            "Dompet kulit sapi asli dengan pelindung RFID, enam slot kartu, dan "
            "profil tipis yang tidak menggembung di kantong."
        ),
        specs=[("Material", "Kulit sapi full grain"), ("Slot Kartu", "6 slot"),
               ("Proteksi", "RFID blocking"), ("Dimensi", "10 x 7.5 x 1 cm"),
               ("Berat", "55 g"), ("Garansi", "6 bulan jahitan")],
        variants=[("BRN", 0, 26, {"color": "Brown"}),
                  ("BLK", 0, 31, {"color": "Black"})],
        image_query="brown leather wallet",
    ),
    dict(
        slug="classic-analog-watch-steel", title="Jam Tangan Analog Stainless Steel",
        brand="uniqlo", category="fashion", price=1_299_000, compare_at=1_699_000,
        badges=["promo"],
        description=(
            "Jam tangan analog dengan gerakan quartz Jepang, kaca sapphire, dan "
            "tali stainless steel mesh yang bisa disesuaikan."
        ),
        specs=[("Gerakan", "Quartz Jepang"), ("Kaca", "Sapphire crystal"),
               ("Tahan Air", "5 ATM"), ("Diameter", "40 mm"),
               ("Tali", "Stainless steel mesh"), ("Garansi", "24 bulan mesin")],
        variants=[("SLV", 0, 12, {"color": "Silver"}),
                  ("GLD", 200_000, 7, {"color": "Gold"})],
        image_query="analog wristwatch steel",
    ),

    # --- Home & Living -----------------------------------------------------
    dict(
        slug="ikea-landskrona-3-seat-sofa", title="IKEA LANDSKRONA Sofa 3 Seater", brand="ikea",
        category="home-living", price=8_000_000, compare_at=9_500_000,
        badges=["promo"],
        description=(
            "Sofa tiga tempat duduk dengan rangka kayu solid, bantalan busa "
            "kepadatan tinggi, dan kaki kayu birch yang bisa dilepas."
        ),
        specs=[("Dimensi", "204 x 89 x 78 cm"), ("Rangka", "Kayu solid + plywood"),
               ("Bantalan", "Busa high resilience"), ("Sarung", "Tidak bisa dilepas"),
               ("Kapasitas", "3 orang"), ("Garansi", "10 tahun rangka")],
        variants=[("GRY", 0, 6, {"color": "Grey", "material": "Fabric"}),
                  ("BRN-LTH", 4_000_000, 3, {"color": "Brown", "material": "Leather"})],
        image_query="grey fabric sofa living room",
    ),
    dict(
        slug="dyson-v15-detect-absolute", title="Dyson V15 Detect Absolute", brand="dyson",
        category="home-living", price=12_000_000, compare_at=13_500_000,
        badges=["premium", "bestseller"],
        description=(
            "Vacuum cordless dengan laser pendeteksi debu halus, sensor piezo "
            "penghitung partikel, dan layar LCD yang menampilkan hasil isap."
        ),
        specs=[("Daya Isap", "230 AW"), ("Baterai", "Hingga 60 menit"),
               ("Filtrasi", "HEPA whole-machine"), ("Kapasitas Bin", "0.77 L"),
               ("Berat", "3.0 kg"), ("Garansi", "24 bulan resmi")],
        variants=[("STD", 0, 8, {"bundle": "Standard"}),
                  ("PLUS", 1_500_000, 4, {"bundle": "Extra battery"})],
        image_query="cordless vacuum cleaner",
    ),
    dict(
        slug="ikea-markus-office-chair", title="IKEA MARKUS Office Chair", brand="ikea",
        category="home-living", price=2_999_000, compare_at=3_499_000,
        badges=["promo"],
        description=(
            "Kursi kerja dengan sandaran mesh tinggi, penyangga leher, dan "
            "mekanisme tilt yang bisa dikunci pada posisi kerja favorit."
        ),
        specs=[("Sandaran", "Mesh tinggi"), ("Penyesuaian", "Tinggi + tilt lock"),
               ("Beban Maks", "110 kg"), ("Material Kaki", "Baja"),
               ("Roda", "5 caster lantai keras"), ("Garansi", "10 tahun")],
        variants=[("BLK", 0, 15, {"color": "Vissle Black"}),
                  ("GRY", 0, 11, {"color": "Vissle Grey"})],
        image_query="ergonomic office chair mesh",
    ),
    dict(
        slug="philips-hue-starter-kit", title="Philips Hue White & Color Starter Kit",
        brand="philips", category="home-living", price=2_499_000, compare_at=2_899_000,
        badges=["new"],
        description=(
            "Paket tiga lampu pintar dengan 16 juta warna, bridge Zigbee, dan "
            "dukungan Google Home, Alexa, serta Apple Home."
        ),
        specs=[("Isi", "3 bohlam E27 + Hue Bridge"), ("Warna", "16 juta + white ambiance"),
               ("Lumen", "1100 lm per bohlam"), ("Protokol", "Zigbee 3.0"),
               ("Integrasi", "Google, Alexa, Apple Home"), ("Garansi", "24 bulan")],
        variants=[("E27-3PK", 0, 12, {"socket": "E27", "pack": "3 bulbs"}),
                  ("GU10-3PK", -300_000, 9, {"socket": "GU10", "pack": "3 spots"})],
        image_query="smart light bulb ambient",
    ),
    dict(
        slug="bosch-series-4-washing-machine", title="Bosch Series 4 Front Load 8kg",
        brand="bosch", category="home-living", price=6_499_000, compare_at=7_299_000,
        badges=["promo"],
        description=(
            "Mesin cuci front loading 8 kg dengan motor EcoSilence Drive, "
            "program AllergyPlus, dan sensor beban otomatis."
        ),
        specs=[("Kapasitas", "8 kg"), ("Putaran", "1200 rpm"),
               ("Motor", "EcoSilence Drive"), ("Program", "15 program"),
               ("Efisiensi", "Kelas A+++"), ("Garansi", "24 bulan + 10 tahun motor")],
        variants=[("8KG-WHT", 0, 7, {"capacity": "8 kg", "color": "White"}),
                  ("9KG-WHT", 1_200_000, 4, {"capacity": "9 kg", "color": "White"})],
        image_query="front load washing machine",
    ),
    dict(
        slug="ceramic-dinnerware-set-16", title="Set Peralatan Makan Keramik 16 Pcs",
        brand="ikea", category="home-living", price=899_000, compare_at=1_150_000,
        badges=["promo"],
        description=(
            "Set makan 16 buah untuk empat orang: piring makan, piring saji, "
            "mangkuk sup, dan mug keramik stoneware yang aman microwave."
        ),
        specs=[("Isi", "4 piring makan, 4 piring saji, 4 mangkuk, 4 mug"),
               ("Material", "Stoneware glazed"), ("Microwave", "Aman"),
               ("Dishwasher", "Aman"), ("Asal", "Indonesia"), ("Garansi", "Pecah saat kirim diganti")],
        variants=[("WHT", 0, 20, {"color": "White"}),
                  ("SGE", 0, 13, {"color": "Sage Green"})],
        image_query="ceramic dinnerware plates set",
    ),
    dict(
        slug="memory-foam-mattress-queen", title="Kasur Memory Foam Queen 160x200",
        brand="ikea", category="home-living", price=4_999_000, compare_at=6_500_000,
        badges=["promo", "bestseller"],
        description=(
            "Kasur memory foam tiga lapis dengan lapisan gel pendingin, sarung "
            "knit yang bisa dilepas, dan tingkat kekerasan medium."
        ),
        specs=[("Ukuran", "160 x 200 cm"), ("Ketebalan", "25 cm"),
               ("Lapisan", "Gel memory foam + HR foam + base"),
               ("Kekerasan", "Medium"), ("Sarung", "Knit removable"),
               ("Garansi", "10 tahun foam")],
        variants=[("Q-160", 0, 9, {"size": "160x200"}),
                  ("K-180", 1_000_000, 5, {"size": "180x200"})],
        image_query="mattress bedroom bed",
    ),

    # --- Beauty ------------------------------------------------------------
    dict(
        slug="the-body-shop-tea-tree-oil", title="The Body Shop Tea Tree Oil 20ml",
        brand="the-body-shop", category="beauty", price=189_000, compare_at=229_000,
        badges=["bestseller"],
        description=(
            "Skincare minyak tea tree murni dari Community Fair Trade Kenya untuk "
            "merawat kulit berjerawat. Oleskan tipis pada area bermasalah."
        ),
        specs=[("Volume", "20 ml"), ("Kandungan", "Tea tree oil Kenya"),
               ("Tipe Kulit", "Berminyak dan berjerawat"), ("Vegan", "Ya"),
               ("Kemasan", "Botol kaca + aplikator"), ("Kedaluwarsa", "24 bulan")],
        variants=[("20ML", 0, 35, {"volume": "20ml"}),
                  ("10ML", -70_000, 42, {"volume": "10ml"})],
        image_query="skincare serum bottle",
    ),
    dict(
        slug="wardah-lightening-day-cream", title="Wardah Lightening Day Cream SPF 30",
        brand="wardah", category="beauty", price=59_000, compare_at=75_000,
        badges=["promo"],
        description=(
            "Skincare pelembap siang dengan SPF 30 PA+++ dan niacinamide untuk "
            "meratakan warna kulit. Formula halal dan ringan tanpa rasa lengket."
        ),
        specs=[("Volume", "30 g"), ("SPF", "SPF 30 PA+++"),
               ("Bahan Aktif", "Niacinamide, vitamin B3"), ("Sertifikasi", "Halal MUI"),
               ("Tipe Kulit", "Normal ke kering"), ("Kedaluwarsa", "36 bulan")],
        variants=[("30G", 0, 60, {"size": "30g"})],
        image_query="face cream jar cosmetic",
    ),
    dict(
        slug="niacinamide-serum-10", title="Serum Niacinamide 10% + Zinc 1%",
        brand="wardah", category="beauty", price=129_000, compare_at=159_000,
        badges=["promo", "bestseller"],
        description=(
            "Skincare serum untuk mengontrol sebum dan memudarkan bekas jerawat. "
            "Tekstur ringan berbasis air, cocok dipakai pagi dan malam."
        ),
        specs=[("Volume", "30 ml"), ("Bahan Aktif", "Niacinamide 10%, Zinc PCA 1%"),
               ("pH", "5.0-6.0"), ("Bebas", "Alkohol dan parfum"),
               ("Tipe Kulit", "Semua, terutama berminyak"), ("Kedaluwarsa", "24 bulan")],
        variants=[("30ML", 0, 48, {"volume": "30ml"}),
                  ("60ML", 90_000, 21, {"volume": "60ml"})],
        image_query="serum dropper skincare",
    ),
    dict(
        slug="matte-liquid-lipstick-set", title="Set Lipstik Cair Matte 4 Warna",
        brand="wardah", category="beauty", price=199_000, compare_at=279_000,
        badges=["promo"],
        description=(
            "Makeup empat lipstik cair matte tahan hingga 8 jam dengan pigmentasi "
            "penuh dan formula yang tidak membuat bibir kering."
        ),
        specs=[("Isi", "4 x 4 ml"), ("Finish", "Matte"),
               ("Ketahanan", "Hingga 8 jam"), ("Transfer Proof", "Ya"),
               ("Sertifikasi", "Halal MUI"), ("Kedaluwarsa", "30 bulan")],
        variants=[("NUDE", 0, 24, {"shade": "Nude Series"}),
                  ("BOLD", 0, 19, {"shade": "Bold Series"})],
        image_query="liquid lipstick makeup",
    ),

    # --- Sports ------------------------------------------------------------
    dict(
        slug="shimano-road-bike-105", title="Sepeda Road Bike Shimano 105 11-Speed",
        brand="shimano", category="sports", price=18_500_000, compare_at=21_000_000,
        badges=["premium"],
        description=(
            "Road bike rangka carbon dengan groupset Shimano 105 11-speed, rem "
            "cakram hidrolik, dan wheelset aero 40 mm."
        ),
        specs=[("Rangka", "Full carbon monocoque"), ("Groupset", "Shimano 105 R7000 11-speed"),
               ("Rem", "Hydraulic disc"), ("Wheelset", "Carbon aero 40 mm"),
               ("Berat", "8.4 kg"), ("Garansi", "5 tahun rangka")],
        variants=[("48", 0, 4, {"frame_size": "48 cm"}),
                  ("51", 0, 6, {"frame_size": "51 cm"}),
                  ("54", 0, 5, {"frame_size": "54 cm"})],
        image_query="road bicycle carbon",
    ),
    dict(
        slug="adidas-yoga-mat-pro", title="Adidas Yoga Mat Pro 6mm", brand="adidas",
        category="sports", price=449_000, compare_at=549_000,
        badges=["promo"],
        description=(
            "Matras yoga TPE 6 mm dua sisi dengan permukaan anti slip dan garis "
            "penanda posisi. Termasuk tali bawa."
        ),
        specs=[("Ketebalan", "6 mm"), ("Dimensi", "183 x 61 cm"),
               ("Material", "TPE bebas lateks"), ("Permukaan", "Anti slip dua sisi"),
               ("Berat", "1.1 kg"), ("Aksesori", "Tali bawa")],
        variants=[("PUR", 0, 28, {"color": "Purple"}),
                  ("GRY", 0, 24, {"color": "Grey"})],
        image_query="yoga mat exercise",
    ),
    dict(
        slug="adjustable-dumbbell-set-24kg", title="Dumbbell Adjustable Set 24kg",
        brand="shimano", category="sports", price=1_899_000, compare_at=2_400_000,
        badges=["promo", "bestseller"],
        description=(
            "Sepasang dumbbell yang bisa diatur 2 sampai 24 kg per sisi dengan "
            "mekanisme dial cepat, cocok untuk latihan di rumah."
        ),
        specs=[("Rentang Beban", "2-24 kg per dumbbell"), ("Mekanisme", "Dial selector"),
               ("Material", "Baja + pelapis karet"), ("Isi", "2 dumbbell + 2 dudukan"),
               ("Dimensi", "42 x 20 x 20 cm"), ("Garansi", "12 bulan")],
        variants=[("24KG-PAIR", 0, 10, {"weight": "24 kg per side"})],
        image_query="dumbbell home gym weights",
    ),
    dict(
        slug="nike-mercurial-football-boots", title="Nike Mercurial Vapor Football Boots",
        brand="nike", category="sports", price=2_299_000, compare_at=2_699_000,
        badges=["new"],
        description=(
            "Sepatu bola dengan upper Vaporposite+ dan sol Zoom Air untuk "
            "akselerasi cepat di lapangan rumput alami."
        ),
        specs=[("Upper", "Vaporposite+"), ("Sol", "Zoom Air FG"),
               ("Permukaan", "Firm ground"), ("Berat", "196 g"),
               ("Stud", "Chevron + blade"), ("Garansi", "3 bulan cacat produksi")],
        variants=[("40", 0, 9, {"size": "40"}),
                  ("41", 0, 12, {"size": "41"}),
                  ("42", 0, 11, {"size": "42"}),
                  ("43", 0, 6, {"size": "43"})],
        image_query="football boots soccer cleats",
    ),

    # --- Toys --------------------------------------------------------------
    dict(
        slug="lego-star-wars-millennium-falcon", title="LEGO Star Wars Millennium Falcon",
        brand="lego", category="toys", price=13_000_000, compare_at=14_500_000,
        badges=["premium", "bestseller"],
        description=(
            "Mainan set koleksi Ultimate Collector Series dengan 7541 bagian, "
            "kokpit detail, dan minifigure klasik maupun sekuel."
        ),
        specs=[("Jumlah Bagian", "7541 pcs"), ("Usia", "16+"),
               ("Dimensi Rakit", "84 x 56 x 21 cm"), ("Minifigure", "10 buah"),
               ("Seri", "Ultimate Collector Series"), ("Garansi", "Bagian hilang diganti")],
        variants=[("UCS", 0, 3, {"edition": "Ultimate Collector Series"})],
        image_query="lego bricks spaceship model",
    ),
    dict(
        slug="lego-city-police-station", title="LEGO City Police Station", brand="lego",
        category="toys", price=1_899_000, compare_at=2_199_000,
        badges=["promo"],
        description=(
            "Mainan set LEGO City berisi kantor polisi, mobil patroli, drone, dan enam "
            "minifigure untuk permainan cerita anak."
        ),
        specs=[("Jumlah Bagian", "668 pcs"), ("Usia", "6+"),
               ("Minifigure", "6 buah"), ("Kendaraan", "Mobil patroli + truk"),
               ("Seri", "LEGO City"), ("Garansi", "Bagian hilang diganti")],
        variants=[("60316", 0, 14, {"set": "60316"})],
        image_query="lego city toy set",
    ),
    dict(
        slug="wooden-montessori-puzzle-set", title="Puzzle Kayu Montessori Set 4 Pcs",
        brand="lego", category="toys", price=299_000, compare_at=399_000,
        badges=["promo"],
        description=(
            "Mainan empat papan puzzle kayu dengan tema angka, huruf, bentuk, dan "
            "hewan. Finishing cat non toksik berbasis air."
        ),
        specs=[("Isi", "4 papan puzzle"), ("Usia", "2-5 tahun"),
               ("Material", "Kayu pinus solid"), ("Cat", "Non toksik water based"),
               ("Dimensi", "30 x 22 cm per papan"), ("Sertifikasi", "SNI mainan")],
        variants=[("SET4", 0, 30, {"pack": "4 boards"})],
        image_query="wooden puzzle toy children",
    ),
    dict(
        slug="rc-drift-car-4wd", title="Mobil RC Drift 4WD Skala 1:16", brand="xiaomi",
        category="toys", price=849_000, compare_at=999_000,
        badges=["promo"],
        description=(
            "Mainan mobil remote control 4WD skala 1:16 dengan ban drift, baterai "
            "isi ulang 1200mAh, dan remote 2.4GHz jarak 50 meter."
        ),
        specs=[("Skala", "1:16"), ("Penggerak", "4WD"),
               ("Baterai", "1200mAh Li-ion"), ("Durasi", "25 menit per charge"),
               ("Remote", "2.4GHz, 50 m"), ("Usia", "8+")],
        variants=[("BLU", 0, 17, {"color": "Blue"}),
                  ("RED", 0, 15, {"color": "Red"})],
        image_query="remote control car toy",
    ),

    # --- Books -------------------------------------------------------------
    dict(
        slug="atomic-habits-indonesia", title="Atomic Habits (Edisi Bahasa Indonesia)",
        brand="gramedia", category="books", price=109_000, compare_at=135_000,
        badges=["bestseller"],
        description=(
            "Buku panduan James Clear untuk membangun kebiasaan baik dan "
            "menghentikan kebiasaan buruk melalui perubahan kecil yang konsisten."
        ),
        specs=[("Penulis", "James Clear"), ("Penerbit", "Gramedia Pustaka Utama"),
               ("Halaman", "352"), ("Bahasa", "Indonesia"),
               ("ISBN", "9786020633176"), ("Sampul", "Soft cover")],
        variants=[("SC", 0, 45, {"cover": "Soft cover"}),
                  ("HC", 60_000, 12, {"cover": "Hard cover"})],
        image_query="books stack reading",
    ),
    dict(
        slug="clean-code-robert-martin", title="Clean Code: A Handbook of Agile Software Craftsmanship",
        brand="gramedia", category="books", price=749_000, compare_at=899_000,
        badges=["promo"],
        description=(
            "Buku rujukan Robert C. Martin tentang menulis kode yang mudah dibaca, "
            "diuji, dan dirawat dalam jangka panjang."
        ),
        specs=[("Penulis", "Robert C. Martin"), ("Penerbit", "Prentice Hall"),
               ("Halaman", "464"), ("Bahasa", "Inggris"),
               ("ISBN", "9780132350884"), ("Sampul", "Paperback")],
        variants=[("PB", 0, 18, {"cover": "Paperback"})],
        image_query="programming book on desk",
    ),
    dict(
        slug="laut-bercerita-leila-chudori", title="Laut Bercerita", brand="gramedia",
        category="books", price=99_000, compare_at=120_000,
        badges=["promo"],
        description=(
            "Buku novel Leila S. Chudori tentang aktivis mahasiswa 1998 dan "
            "keluarga yang ditinggalkan, ditulis dari dua sudut pandang."
        ),
        specs=[("Penulis", "Leila S. Chudori"), ("Penerbit", "Kepustakaan Populer Gramedia"),
               ("Halaman", "394"), ("Bahasa", "Indonesia"),
               ("ISBN", "9786024246945"), ("Sampul", "Soft cover")],
        variants=[("SC", 0, 33, {"cover": "Soft cover"})],
        image_query="indonesian novel book",
    ),
    dict(
        slug="dot-grid-notebook-a5", title="Notebook Dot Grid A5 160gsm", brand="gramedia",
        category="books", price=89_000, compare_at=110_000,
        badges=["promo"],
        description=(
            "Buku catatan dot grid A5 dengan kertas 160 gsm anti tembus tinta, "
            "jilid lay flat, dan dua pembatas kain."
        ),
        specs=[("Ukuran", "A5 (148 x 210 mm)"), ("Kertas", "160 gsm dot grid"),
               ("Halaman", "192"), ("Jilid", "Lay flat stitched"),
               ("Pembatas", "2 ribbon"), ("Sampul", "Hard cover")],
        variants=[("BLK", 0, 40, {"color": "Black"}),
                  ("TAN", 0, 26, {"color": "Tan"})],
        image_query="notebook journal stationery",
    ),

    # --- Automotive --------------------------------------------------------
    dict(
        slug="bosch-car-battery-din55", title="Bosch Aki Mobil DIN55 55Ah", brand="bosch",
        category="automotive", price=1_450_000, compare_at=1_699_000,
        badges=["promo"],
        description=(
            "Aki mobil bebas perawatan 55Ah dengan teknologi PowerFrame dan arus "
            "start dingin 480A, cocok untuk sedan dan MPV kompak."
        ),
        specs=[("Kapasitas", "55 Ah"), ("CCA", "480 A"),
               ("Tipe", "Maintenance free"), ("Teknologi", "PowerFrame grid"),
               ("Dimensi", "242 x 175 x 190 mm"), ("Garansi", "12 bulan")],
        variants=[("DIN55", 0, 12, {"type": "DIN55"}),
                  ("DIN66", 400_000, 7, {"type": "DIN66"})],
        image_query="car battery automotive part",
    ),
    dict(
        slug="dashcam-4k-dual-lens", title="Dashcam 4K Dual Lens dengan GPS", brand="xiaomi",
        category="automotive", price=1_299_000, compare_at=1_599_000,
        badges=["promo", "new"],
        description=(
            "Kamera mobil depan 4K dan belakang 1080p dengan GPS logger, mode "
            "parkir 24 jam, dan Wi-Fi untuk unduh rekaman ke ponsel."
        ),
        specs=[("Resolusi Depan", "3840x2160 30fps"), ("Resolusi Belakang", "1920x1080"),
               ("GPS", "Terintegrasi"), ("Mode Parkir", "24 jam dengan hardwire kit"),
               ("Penyimpanan", "microSD hingga 256GB"), ("Garansi", "12 bulan")],
        variants=[("DUAL", 0, 16, {"setup": "Front + rear"}),
                  ("FRONT", -400_000, 21, {"setup": "Front only"})],
        image_query="dashcam car camera",
    ),
    dict(
        slug="car-vacuum-cordless-mini", title="Vacuum Mobil Cordless Mini 9000Pa",
        brand="xiaomi", category="automotive", price=449_000, compare_at=599_000,
        badges=["promo"],
        description=(
            "Penyedot debu mobil tanpa kabel dengan daya isap 9000Pa, filter HEPA "
            "yang bisa dicuci, dan tiga nozzle untuk celah sempit."
        ),
        specs=[("Daya Isap", "9000 Pa"), ("Baterai", "2500mAh"),
               ("Durasi", "30 menit"), ("Filter", "HEPA washable"),
               ("Aksesori", "3 nozzle"), ("Garansi", "12 bulan")],
        variants=[("STD", 0, 25, {"bundle": "Standard"})],
        image_query="handheld car vacuum",
    ),
    dict(
        slug="microfiber-car-care-kit", title="Paket Perawatan Mobil Microfiber 7 Pcs",
        brand="bosch", category="automotive", price=249_000, compare_at=329_000,
        badges=["promo"],
        description=(
            "Paket cuci dan detailing berisi kain microfiber, sarung wax, sikat "
            "ban, serta shampo pH netral konsentrat."
        ),
        specs=[("Isi", "4 kain microfiber, 1 wax applicator, 1 sikat, 1 shampo"),
               ("Shampo", "500 ml konsentrat pH netral"), ("GSM Kain", "350 gsm"),
               ("Aman", "Tidak menggores clear coat"), ("Cuci Ulang", "Hingga 200x"),
               ("Asal", "Indonesia")],
        variants=[("KIT7", 0, 34, {"pack": "7 pcs"})],
        image_query="car cleaning microfiber cloth",
    ),

    # --- Health ------------------------------------------------------------
    dict(
        slug="omron-blood-pressure-monitor", title="Omron Tensimeter Digital Lengan Atas",
        brand="philips", category="health", price=899_000, compare_at=1_099_000,
        badges=["bestseller"],
        description=(
            "Alat ukur tekanan darah digital dengan deteksi detak tidak teratur, "
            "memori 60 pembacaan, dan manset lengan 22-42 cm."
        ),
        specs=[("Metode", "Oscillometric"), ("Rentang", "0-299 mmHg"),
               ("Memori", "60 pembacaan"), ("Manset", "22-42 cm"),
               ("Daya", "4 baterai AA atau adaptor"), ("Garansi", "24 bulan")],
        variants=[("STD", 0, 18, {"bundle": "Standard"}),
                  ("ADP", 150_000, 9, {"bundle": "With adaptor"})],
        image_query="blood pressure monitor medical",
    ),
    dict(
        slug="whey-protein-isolate-2lb", title="Whey Protein Isolate 2 lbs", brand="wardah",
        category="health", price=649_000, compare_at=799_000,
        badges=["promo", "bestseller"],
        description=(
            "Protein isolat 27 g per sajian dengan 5.5 g BCAA, rendah laktosa, "
            "dan mudah larut dalam air maupun susu."
        ),
        specs=[("Berat", "2 lbs (907 g)"), ("Protein", "27 g per sajian"),
               ("BCAA", "5.5 g per sajian"), ("Sajian", "30 servis"),
               ("Laktosa", "Rendah"), ("Kedaluwarsa", "18 bulan")],
        variants=[("CHOC", 0, 26, {"flavor": "Chocolate"}),
                  ("VAN", 0, 21, {"flavor": "Vanilla"}),
                  ("MOC", 0, 14, {"flavor": "Mocha"})],
        image_query="protein powder supplement",
    ),
    dict(
        slug="vitamin-d3-1000iu-90", title="Vitamin D3 1000 IU 90 Softgel", brand="wardah",
        category="health", price=139_000, compare_at=179_000,
        badges=["promo"],
        description=(
            "Suplemen vitamin D3 1000 IU untuk membantu kesehatan tulang dan "
            "sistem imun. Satu softgel per hari sesudah makan."
        ),
        specs=[("Isi", "90 softgel"), ("Dosis", "1000 IU per softgel"),
               ("Sumber", "Cholecalciferol"), ("Konsumsi", "1x sehari"),
               ("Sertifikasi", "BPOM"), ("Kedaluwarsa", "24 bulan")],
        variants=[("90", 0, 40, {"count": "90 softgel"}),
                  ("180", 110_000, 18, {"count": "180 softgel"})],
        image_query="vitamin supplement bottle",
    ),
    dict(
        slug="digital-body-fat-scale", title="Timbangan Digital Body Fat Bluetooth",
        brand="xiaomi", category="health", price=349_000, compare_at=449_000,
        badges=["promo"],
        description=(
            "Timbangan pintar yang mengukur berat, massa otot, lemak tubuh, dan "
            "air. Sinkron ke aplikasi lewat Bluetooth untuk 16 profil."
        ),
        specs=[("Kapasitas", "180 kg"), ("Presisi", "50 g"),
               ("Metrik", "13 indikator komposisi tubuh"), ("Konektivitas", "Bluetooth 5.0"),
               ("Profil", "16 pengguna"), ("Garansi", "12 bulan")],
        variants=[("WHT", 0, 29, {"color": "White"}),
                  ("BLK", 0, 22, {"color": "Black"})],
        image_query="digital bathroom scale",
    ),

    # --- Garden ------------------------------------------------------------
    dict(
        slug="bosch-cordless-grass-trimmer", title="Bosch Cordless Grass Trimmer 18V",
        brand="bosch", category="garden", price=1_749_000, compare_at=2_100_000,
        badges=["promo"],
        description=(
            "Alat kebun pemangkas rumput baterai 18V dengan lebar potong 26 cm, "
            "kepala yang bisa diputar 90 derajat, dan sistem ganti benang semi "
            "otomatis."
        ),
        specs=[("Tegangan", "18 V"), ("Lebar Potong", "26 cm"),
               ("Baterai", "2.5Ah termasuk"), ("Berat", "2.4 kg"),
               ("Kepala", "Rotasi 90 derajat"), ("Garansi", "24 bulan")],
        variants=[("1BAT", 0, 11, {"bundle": "1 battery"}),
                  ("2BAT", 500_000, 6, {"bundle": "2 batteries"})],
        image_query="garden grass trimmer tool",
    ),
    dict(
        slug="terracotta-planter-set-3", title="Set Pot Terakota 3 Ukuran", brand="ikea",
        category="garden", price=249_000, compare_at=319_000,
        badges=["promo"],
        description=(
            "Tiga pot terakota dengan lubang drainase dan tatakan terpisah. "
            "Pori alami tanah liat membantu akar bernapas."
        ),
        specs=[("Isi", "3 pot + 3 tatakan"), ("Diameter", "12, 16, 20 cm"),
               ("Material", "Terakota bakar"), ("Drainase", "Lubang bawah"),
               ("Penggunaan", "Indoor dan outdoor"), ("Asal", "Indonesia")],
        variants=[("SET3", 0, 32, {"pack": "3 pots"})],
        image_query="terracotta plant pots",
    ),
    dict(
        slug="garden-hose-reel-20m", title="Selang Taman 20m dengan Reel", brand="bosch",
        category="garden", price=549_000, compare_at=699_000,
        badges=["promo"],
        description=(
            "Selang taman 20 meter tiga lapis anti kusut dengan reel berdiri, "
            "nozzle tujuh pola, dan konektor cepat."
        ),
        specs=[("Panjang", "20 m"), ("Diameter", "1/2 inci"),
               ("Lapisan", "3 lapis anti kink"), ("Tekanan Maks", "8 bar"),
               ("Aksesori", "Nozzle 7 pola + konektor"), ("Garansi", "12 bulan")],
        variants=[("20M", 0, 15, {"length": "20 m"}),
                  ("30M", 200_000, 8, {"length": "30 m"})],
        image_query="garden hose watering",
    ),
    dict(
        slug="hand-garden-tool-set-5", title="Set Alat Kebun Stainless 5 Pcs", brand="bosch",
        category="garden", price=329_000, compare_at=429_000,
        badges=["promo"],
        description=(
            "Lima alat kebun tangan berbahan stainless steel dengan pegangan kayu "
            "dan tas kanvas penyimpanan."
        ),
        specs=[("Isi", "Sekop, garpu, penggaruk, transplanter, gunting"),
               ("Material", "Stainless steel"), ("Pegangan", "Kayu solid"),
               ("Tas", "Kanvas tahan air"), ("Berat Total", "1.3 kg"),
               ("Garansi", "6 bulan")],
        variants=[("SET5", 0, 27, {"pack": "5 tools"})],
        image_query="gardening hand tools",
    ),
]
