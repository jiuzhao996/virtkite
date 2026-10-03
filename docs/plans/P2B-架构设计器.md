# P2B · eNSP 式架构设计器（画布→部署计划→一键生成）

## 目标
画布拖拽 VM 角色/容器/网络节点 → 连线 → 配置 → 生成部署计划 YAML → 执行器落地。

## 技术选型
前端画布：AntV X6（成熟、Vue3 支持好）或 Vue Flow；先 X6。
节点类型：VM(模板/规格/应用)、容器(引用 stack service)、网络(新建 libvirt net / docker net)、连线=通信关系（部署时校验网段可达）。

## 部署计划 YAML（设计器产物，plan/*.yml）
name/nodes[{id,type,vm_spec|service,apps[]}]/links[{a,b}]/notes
执行器（后端，逐节点串行+依赖拓扑）：建 VM(克隆/新建+cloud-init)→等 IP→SSH 应用安装→compose 段落 up。全程后台任务+进度流（复用 tasks 体系）。

## 里程碑
M1 只读画布渲染既有平台资源为图（拓扑复用）；M2 可编辑+导出 YAML；M3 执行器+一键部署；M4 模板库（"ES 集群架构"一键放画布）。
答辩演示位：M3。

## 风险
工程量最大（估 4-6 个工作日）；画布交互细节多；先 M1/M2 验收再投 M3。
