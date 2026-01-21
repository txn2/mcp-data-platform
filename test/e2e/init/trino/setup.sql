-- E2E Test Trino Setup
-- Creates test tables in the memory catalog

-- Create schema if not exists
CREATE SCHEMA IF NOT EXISTS memory.e2e_test;

-- Test orders table (with full metadata in DataHub)
CREATE TABLE IF NOT EXISTS memory.e2e_test.test_orders (
    order_id BIGINT,
    customer_id BIGINT,
    order_date DATE,
    total_amount DECIMAL(10, 2),
    status VARCHAR(50)
);

-- Insert sample data
INSERT INTO memory.e2e_test.test_orders VALUES
    (1, 100, DATE '2024-01-15', 150.00, 'completed'),
    (2, 101, DATE '2024-01-16', 250.50, 'completed'),
    (3, 100, DATE '2024-01-17', 75.25, 'pending'),
    (4, 102, DATE '2024-01-18', 320.00, 'completed'),
    (5, 103, DATE '2024-01-19', 180.75, 'cancelled');

-- Legacy users table (deprecated in DataHub)
CREATE TABLE IF NOT EXISTS memory.e2e_test.legacy_users (
    user_id BIGINT,
    username VARCHAR(100),
    email VARCHAR(255),
    created_at TIMESTAMP
);

-- Insert sample data
INSERT INTO memory.e2e_test.legacy_users VALUES
    (100, 'alice', 'alice@example.com', TIMESTAMP '2023-06-01 10:00:00'),
    (101, 'bob', 'bob@example.com', TIMESTAMP '2023-06-15 11:30:00'),
    (102, 'charlie', 'charlie@example.com', TIMESTAMP '2023-07-01 09:00:00'),
    (103, 'diana', 'diana@example.com', TIMESTAMP '2023-07-20 14:45:00');

-- Products table (no DataHub metadata - tests no enrichment case)
CREATE TABLE IF NOT EXISTS memory.e2e_test.products (
    product_id BIGINT,
    name VARCHAR(255),
    price DECIMAL(10, 2),
    category VARCHAR(100)
);

-- Insert sample data
INSERT INTO memory.e2e_test.products VALUES
    (1, 'Widget A', 29.99, 'widgets'),
    (2, 'Widget B', 49.99, 'widgets'),
    (3, 'Gadget X', 199.99, 'gadgets'),
    (4, 'Gadget Y', 149.99, 'gadgets');

-- Customer metrics table (for query enrichment tests)
CREATE TABLE IF NOT EXISTS memory.e2e_test.customer_metrics (
    customer_id BIGINT,
    total_orders INTEGER,
    total_spent DECIMAL(12, 2),
    first_order_date DATE,
    last_order_date DATE
);

-- Insert sample data
INSERT INTO memory.e2e_test.customer_metrics VALUES
    (100, 2, 225.25, DATE '2024-01-15', DATE '2024-01-17'),
    (101, 1, 250.50, DATE '2024-01-16', DATE '2024-01-16'),
    (102, 1, 320.00, DATE '2024-01-18', DATE '2024-01-18'),
    (103, 1, 180.75, DATE '2024-01-19', DATE '2024-01-19');
