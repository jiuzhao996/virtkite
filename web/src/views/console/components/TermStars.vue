<template>
  <div class="stars-bg">
    <div v-for="(s, i) in STARS" :key="i" class="star" :style="{
      left: s.left + '%',
      top: s.top + '%',
      animationDelay: s.delay + 's',
      animationDuration: s.duration + 's',
    }" />
  </div>
</template>

<script setup>
// 终端视图的「星星划过」装饰背景。坐标来自一次性生成的模块级常量 STARS：
// 此前在模板里内联 Math.random()，每秒时钟 ref 更新触发重渲染 → 40 颗星每秒重新随机、
// 肉眼可见地“瞬移”；改为模块级常量后 v-for 只读渲染，位置/节奏在生命周期内恒定。
// 纯装饰：无 props、无事件，pointer-events:none 不拦截交互。
const STARS = Array.from({ length: 40 }, () => ({
  left: +(Math.random() * 100).toFixed(2),
  top: +(Math.random() * 100).toFixed(2),
  delay: +(Math.random() * 6).toFixed(2),
  duration: +(2 + Math.random() * 4).toFixed(2),
}))
</script>

<style scoped>
/* 以下规则自 TermView.vue 原样搬入（类名不变，DOM 不变） */
.stars-bg { position: absolute; inset: 0; pointer-events: none; overflow: hidden; z-index: 0; }
.star {
  position: absolute;
  width: 2px;
  height: 2px;
  background: #fff; /* 星点恒白（装饰，与主题无关） */
  border-radius: 50%;
  opacity: 0;
  animation: shootingStar linear infinite;
  box-shadow: 0 0 4px 1px var(--term-glow);
}
@keyframes shootingStar {
  0% { opacity: 0; transform: translateX(0) translateY(0); }
  5% { opacity: 1; }
  20% { opacity: 0; transform: translateX(-120px) translateY(80px); }
  100% { opacity: 0; transform: translateX(-120px) translateY(80px); }
}
</style>
