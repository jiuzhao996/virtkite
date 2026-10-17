// 轻组件单测：PageHead 是全站页头骨架，标题/副标题/动作区插槽的契约要锁住。
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import PageHead from './PageHead.vue'

describe('PageHead', () => {
  it('渲染标题与副标题', () => {
    const w = mount(PageHead, { props: { title: '虚拟机', subtitle: '共 3 台' } })
    expect(w.find('.page-title').text()).toBe('虚拟机')
    expect(w.find('.page-desc').text()).toBe('共 3 台')
  })

  it('inline 时副标题与标题同行', () => {
    const w = mount(PageHead, { props: { title: 't', subtitle: 's', inline: true } })
    expect(w.find('.ph-inline').exists()).toBe(true)
    expect(w.find('.page-desc-inline').exists()).toBe(true)
  })

  it('默认插槽渲染右侧动作区', () => {
    const w = mount(PageHead, {
      props: { title: 't' },
      slots: { default: '<button class="act">新建</button>' },
    })
    expect(w.find('button.act').text()).toBe('新建')
  })

  it('无副标题时不渲染描述节点', () => {
    const w = mount(PageHead, { props: { title: 't' } })
    expect(w.find('.page-desc').exists()).toBe(false)
  })
})
