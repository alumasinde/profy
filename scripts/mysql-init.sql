-- Creates the disposable integration-test database for local Docker Compose.
CREATE DATABASE IF NOT EXISTS hudumalink_test CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
GRANT ALL PRIVILEGES ON hudumalink_test.* TO 'huduma'@'%';
