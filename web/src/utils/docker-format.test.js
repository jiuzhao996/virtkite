// docker-format.js 单测：容器/镜像展示口径（状态、端口、大小、相对时间）。
import { describe, it, expect } from 'vitest'
import {
  containerName,
  stateTag,
  stateText,
  composeTag,
  portsText,
  shortId,
  imageTime,
  dockerSize,
} from './docker-format'

describe('容器名与状态', () => {
  it('数组与字符串都能归一', () => {
    expect(containerName(['web', 'db'])).toBe('web, db')
    expect(containerName([])).toBe('—')
    expect(containerName('web')).toBe('web')
    expect(containerName(null)).toBe('—')
  })

  it('状态文案与标签色', () => {
    expect(stateText('running')).toBe('运行中')
    expect(stateText('exited')).toBe('已退出')
    expect(stateText('brand-new')).toBe('brand-new') // 未知状态原样暴露
    expect(stateText('')).toBe('未知')
    expect(stateTag('running')).toBe('success')
    expect(stateTag('exited')).toBe('info')
    expect(stateTag('paused')).toBe('warning')
  })

  it('compose 状态按前缀判色', () => {
    expect(composeTag('running(2)')).toBe('success')
    expect(composeTag('exited(1)')).toBe('info')
    expect(composeTag('stopped')).toBe('info')
    expect(composeTag('partial')).toBe('warning')
  })
})

describe('端口展示', () => {
  it('兼容字符串与对象数组', () => {
    expect(portsText('0.0.0.0:8080->80/tcp')).toBe('0.0.0.0:8080->80/tcp')
    expect(
      portsText([
        { IP: '0.0.0.0', PublicPort: 8080, PrivatePort: 80, Type: 'tcp' },
        { PrivatePort: 443, Type: 'tcp' },
      ])
    ).toBe('0.0.0.0:8080->80/tcp  443/tcp')
    expect(portsText([])).toBe('—')
    expect(portsText(null)).toBe('—')
  })
})

describe('ID / 大小 / 时间', () => {
  it('shortId 去掉 sha256: 前缀并截 12 位', () => {
    expect(shortId('sha256:0123456789abcdef')).toBe('0123456789ab')
    expect(shortId('')).toBe('—')
    expect(shortId(null)).toBe('—')
  })

  it('dockerSize 兼容字节与字符串', () => {
    expect(dockerSize(1024 ** 3)).toBe('1.0 GB')
    expect(dockerSize('1.2GB')).toBe('1.2GB')
    expect(dockerSize(null)).toBe('—')
  })

  it('imageTime 把 docker 相对时间翻成中文', () => {
    expect(imageTime('3 days ago')).toBe('3 天前')
    expect(imageTime('About an hour ago')).toBe('约 1 小时前')
    expect(imageTime('')).toBe('—')
  })
})
