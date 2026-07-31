-- Seed admin and customer users matching toko/src/mocks/data.ts SEED_USERS
-- Idempotent: upserts on email

BEGIN;

-- Hash for "password123" using bcrypt (cost 10)
-- Generated with: bcrypt.hashSync('password123', 10)
INSERT INTO users (name, email, password_hash, roles)
VALUES
  ('Admin User', 'admin@toko.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['admin']),
  ('Noah Developer', 'noah@toko.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['admin']),
  ('Budi Santoso', 'budi@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Siti Aminah', 'siti@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Andi Pratama', 'andi@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Dewi Lestari', 'dewi@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Eko Kurniawan', 'eko@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Fajar Nugraha', 'fajar@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Gita Pertiwi', 'gita@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Hendra Wijaya', 'hendra@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Indah Sari', 'indah@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer']),
  ('Joko Widodo', 'joko@example.com', '$argon2id$v=19$m=65536,t=1,p=8$+mDMDRHMbSFiWfl2PbBX7g$ZpFCFV584H9pVGp8fX5JAiAXVkOz41BaVXNxv/ACR6I', ARRAY['customer'])
ON CONFLICT (email) DO UPDATE SET
  name = EXCLUDED.name,
  password_hash = EXCLUDED.password_hash,
  roles = EXCLUDED.roles,
  updated_at = now();

COMMIT;