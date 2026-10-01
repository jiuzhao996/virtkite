// 终端配色的单一事实源：VM 控制台（views/console/components/TermView）与容器终端
// （components/ContainerTerminal）共用，此前两处各抄一份 16 色 hex。
// xterm 的 theme 需要 canvas 可解析的具体色值（不能引用 CSS var），故以常量集中，
// 与 global.css 的 --term-* 令牌组同源（GitHub Dark 调色板）。
//
// 刻意不随明暗主题反转：终端在两种主题下恒为深色（太空/控制台风），这是设计决定。

// ANSI 16 色（GitHub Dark）
export const ANSI = {
  black: '#1b2838', red: '#f85149', green: '#3fb950', yellow: '#d2991d',
  blue: '#58a6ff', magenta: '#bc8cff', cyan: '#39c5cf', white: '#b1bac4',
  brightBlack: '#30363d', brightRed: '#ff6e6a', brightGreen: '#56d364',
  brightYellow: '#e3b341', brightBlue: '#79c0ff', brightMagenta: '#d2a8ff',
  brightCyan: '#56d4dd', brightWhite: '#f0f6fc'
}

export const TERM_CURSOR = '#58a6ff'
export const TERM_SELECTION = 'rgba(31, 58, 95, 0.7)'

// 两个终端只差「表面」：VM 控制台悬浮在星空背景图上用半透明底（透出背景），
// 容器终端无背景图用实底。
export const VM_TERM_SURFACE = { background: 'rgba(10, 22, 40, 0.18)', foreground: '#e6edf3' }
export const DOCKER_TERM_SURFACE = { background: '#0d1b2a', foreground: '#cfe8ff' }

/**
 * 组装 xterm Terminal({ theme }) 对象。
 * @param {{background: string, foreground: string}} surface 表面配色（VM_TERM_SURFACE / DOCKER_TERM_SURFACE）
 */
export function buildTermTheme(surface) {
  return { ...surface, cursor: TERM_CURSOR, selectionBackground: TERM_SELECTION, ...ANSI }
}
