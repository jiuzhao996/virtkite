---
layout: home

hero:
  name: 鸢航 VirtKite
  text: 基于 KVM 的轻量级私有云管理平台
  tagline: 一个浏览器管住所有虚拟机 —— 创建、控制台、监控、授权、审计，一条龙。原生终端也能直连（SSH 跳板）。
  actions:
    - theme: brand
      text: 10 分钟快速开始 →
      link: /quick-start
    - theme: alt
      text: 先看懂几个概念
      link: /concepts
    - theme: alt
      text: 从 0 到 1 构建之旅
      link: /journey

features:
  - icon: 🖥️
    title: 虚拟机全生命周期
    details: 四步向导创建（ISO / 云镜像 / 增量克隆）、开关机重启、硬件热调整、快照回滚、导出导入——不用再敲一条 virsh 命令。
  - icon: 🪟
    title: 三种控制台
    details: VNC 图形界面 / SSH 终端 / 串口，浏览器里全搞定。机器连不上网也能靠串口救回来。
  - icon: 🔐
    title: 4A 教学闭环
    details: 学生自助申请 → 教师审批限时授权 → 任意终端 SSH 直连 → 危险命令拦截 → 全程审计。对标堡垒机，为教学场景而生。
  - icon: 📊
    title: 监控告警一条龙
    details: 内置 Prometheus 指标、Grafana 看板、9 条告警规则、飞书/钉钉推送——部署完就有，不用自己搭。
  - icon: 🐳
    title: 容器与应用商店
    details: Docker 容器/镜像/网络/卷/编排统一管理；20 个声明式应用（nginx/mysql/gitea…）一键装进虚拟机。
  - icon: 🪁
    title: 单二进制，轻量部署
    details: Go 编译单文件 + 4 个基础设施容器，一台 8G 内存的机器就能跑起来。
