// ESLint 扁平配置（ESLint 9/10 默认格式）。
// 目的：给前端补一条静态检查线。项目此前没有任何 lint，
// 首次引入时只启用「真问题」类规则（未定义变量、重复键、无效转义等），
// 纯风格类规则（缩进/引号/分号）保持关闭——代码风格已由现有代码统一，
// 再开风格规则只会制造几千条无意义告警，掩盖真正的缺陷。
import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'
import globals from 'globals'

export default [
  // 构建产物与依赖目录不参与检查
  { ignores: ['dist/**', 'node_modules/**', 'public/**'] },

  js.configs.recommended,
  ...pluginVue.configs['flat/recommended'],

  {
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: {
        ...globals.browser,
        ...globals.es2021,
      },
    },
    rules: {
      // —— 风格类：关闭 ——
      // 这些在项目里已有稳定写法，开启只会刷屏，且与 prettier 职责重叠
      'vue/max-attributes-per-line': 'off',
      'vue/singleline-html-element-content-newline': 'off',
      'vue/html-self-closing': 'off',
      'vue/html-indent': 'off',
      'vue/html-closing-bracket-newline': 'off',
      'vue/attributes-order': 'off',
      'vue/first-attribute-linebreak': 'off',
      'vue/multiline-html-element-content-newline': 'off',
      'vue/html-quotes': 'off',

      // —— 真问题类：保留 ——
      // 未使用变量降为 warn：模板里通过 <script setup> 绑定的变量
      // 有时 ESLint 静态分析看不到用法（如动态组件名），报 error 会挡住流程。
      // caughtErrors:'none'：`catch (e) {}` 里的 e 是本项目的既定写法
      // （静默降级、清理收尾时吞掉关闭异常），不当作未使用变量处理。
      'no-unused-vars': ['warn', { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrors: 'none' }],
      // 空 catch 块是有意的（关闭/清理失败无处可报，且不应打断主流程）
      'no-empty': ['error', { allowEmptyCatch: true }],
      // 该规则只提示「初始化值随后必被覆盖」，纯风格；`let x = ''` 作为兜底默认值是常见写法
      'no-useless-assignment': 'off',
      // 组件名多单词：本项目有 Login/Logs/Dashboard 等单词页面组件，
      // 且路由名与文件名一一对应，强制多单词只会逼出无意义后缀
      'vue/multi-word-component-names': 'off',
      // 属性顺序/必需 prop 默认值属于风格建议
      'vue/require-default-prop': 'off',
      'vue/require-explicit-emits': 'warn',
      // 允许 v-html（本项目已用 DOMPurify 清洗 AI 回复，见 AiMessageList）
      'vue/no-v-html': 'off',
      // prop 就地修改：本项目建机向导/存储弹窗等把父级 reactive 对象整体作为 prop 下传，
      // 子组件直接改字段（Vue 对象按引用传递，运行正常但违反单向数据流）。
      // 彻底修需改成 emit 事件或 provide/inject，属于前端重构，且必须逐屏目视验收；
      // 已与用户约定推迟，此处关闭规则并留档，避免 110 条告警淹没真正的问题。
      'vue/no-mutating-props': 'off',
    },
  },
]
