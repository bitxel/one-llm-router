ALTER TABLE request_records RENAME COLUMN upstream_response_body TO response_body;
ALTER TABLE request_records DROP COLUMN upstream_request_body;
ALTER TABLE request_records RENAME COLUMN client_request_body TO request_body;
