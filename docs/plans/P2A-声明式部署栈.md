# P2A · 声明式一键部署（Stacks·YAML 容器栈）

## 目标
应用商店升级为"栈商店"：YAML 声明 → 生成 compose → dockerx compose up 一键部署，每栈挂参考笔记。

## 栈清单（stacks/*.yml，git 跟踪 + 磁盘可增）
字段：name/desc/category(中间件/监控/Web/大数据/安全)/docs[笔记路径]/services(标准 compose services)/networks/volumes
首批（从 ~/data/Firefly-6.16.5 笔记提取，子 agent 批量转换，人工抽检）：
ES 三节点集群(Elk01-06)、Kafka+ZK、ELK 单机、Zabbix(Zabbix01-03)、LNMP、
Jumpserver(Web/Other/Jumpserver.md)、Tomcat(Tomcat01-03)、Nginx 集群(Nginx01-10)、
Prometheus 栈(Prom01-05，自托管第二套)、MySQL 主从(MySQL 笔记)

## 后端
- GET /api/stacks（清单+状态：检测已部署=compose ls 比对 project name）
- POST /api/stacks/:id/deploy {pool?}（admin）→ 写 data/stacks/<name>/docker-compose.yml → dockerx compose up -d（复用现有 executor 风格，转后台任务）
- DELETE 下线 = compose down（复用 ComposeTab 现有动作）

## 前端
- 应用商店页改造：单应用(SH) + 部署栈(compose) 两 tab；栈卡片=名称/包含服务徽标/分类/参考笔记链接/部署按钮+状态
- 笔记渲染：docs 字段读 md 原文（后端读文件返回，前端 markdown 渲染，marked 已有）

## 明确不做（本阶段）
跨 VM 编排（→P2B）、ansible 执行器（SSH 脚本已覆盖）、栈参数化表单（v2）
