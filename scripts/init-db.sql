-- 创建数据库
CREATE DATABASE IF NOT EXISTS vmops
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

-- 创建用户
CREATE USER IF NOT EXISTS 'vmops'@'localhost' IDENTIFIED BY 'vmops123';
CREATE USER IF NOT EXISTS 'vmops'@'%' IDENTIFIED BY 'vmops123';

-- 授权
GRANT ALL PRIVILEGES ON vmops.* TO 'vmops'@'localhost';
GRANT ALL PRIVILEGES ON vmops.* TO 'vmops'@'%';

-- 刷新权限
FLUSH PRIVILEGES;
