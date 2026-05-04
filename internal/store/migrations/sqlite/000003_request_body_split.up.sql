ALTER TABLE request_records RENAME COLUMN request_body TO client_request_body;
ALTER TABLE request_records ADD COLUMN upstream_request_body TEXT;
ALTER TABLE request_records RENAME COLUMN response_body TO upstream_response_body;
