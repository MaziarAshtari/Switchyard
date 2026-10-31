-- Automatic re-activation of keys disabled by 401/402/403 responses.

-- When an automatically disabled key becomes active again (Unix ms); 0 = only by reset.
ALTER TABLE api_keys ADD COLUMN disabled_until INTEGER NOT NULL DEFAULT 0;

-- Recovery period for the provider's keys in minutes; NULL = use the global setting.
ALTER TABLE providers ADD COLUMN disabled_key_recovery_minutes INTEGER;
