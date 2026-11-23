-- Optional dashboard password per user (PBKDF2-SHA256, salted). NULL means no password.
ALTER TABLE users ADD COLUMN password_hash TEXT;
