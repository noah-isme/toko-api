CREATE TABLE tenant_memberships (
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'CUSTOMER' CHECK (role IN ('OWNER', 'ADMIN', 'CUSTOMER')),
  status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('INVITED', 'ACTIVE', 'SUSPENDED')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX idx_tenant_memberships_user ON tenant_memberships(user_id, status);

-- Existing installations have one seeded/default tenant and users with global
-- roles. Preserve access while moving authorization to the tenant boundary.
INSERT INTO tenant_memberships (tenant_id, user_id, role, status)
SELECT t.id,
       u.id,
       CASE WHEN 'admin' = ANY(u.roles) THEN 'OWNER' ELSE 'CUSTOMER' END,
       'ACTIVE'
FROM tenants t
JOIN users u ON true
WHERE t.slug = 'default'
ON CONFLICT (tenant_id, user_id) DO NOTHING;
