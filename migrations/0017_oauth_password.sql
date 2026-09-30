CREATE TABLE oauth_password (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  password_hash text NOT NULL CHECK (password_hash LIKE '$argon2id$%')
);
