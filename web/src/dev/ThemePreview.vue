<template>
  <!--
    控件预览页骨架（按 docs/_shared/ui-ux/guidelines/06-控件预览页规范.md）
    接入方式：在路由中登记 path: '/dev/theme-preview'，仅 dev 环境注册；
    或参考本文件结构在合适位置渲染。
    依赖：项目自研 Nt* 组件族（NtButton / NtInput / NtSelect / NtSwitch / NtModal 等）。
  -->
  <div class="theme-preview">
    <header class="theme-preview__head">
      <h1>Theme Preview · 控件陈列室</h1>
      <button type="button" @click="toggleTheme">切换暗/亮</button>
    </header>

    <!-- ① 层级 -->
    <section class="theme-preview__section" aria-labelledby="p-hierarchy">
      <h2 id="p-hierarchy">① 层级（Hierarchy）</h2>
      <div class="theme-preview__row">
        <button class="btn btn--primary">主操作</button>
        <button class="btn btn--secondary">次操作</button>
        <button class="btn btn--text">文字按钮</button>
        <button class="btn btn--danger">危险操作</button>
        <button class="btn btn--primary" disabled>禁用</button>
        <button class="btn btn--primary" aria-busy="true">加载中…</button>
      </div>
    </section>

    <!-- ② 选择 -->
    <section class="theme-preview__section" aria-labelledby="p-choices">
      <h2 id="p-choices">② 选择（Choices）</h2>
      <div class="theme-preview__row">
        <span class="chip chip--success">已完成</span>
        <span class="chip chip--warning">待处理</span>
        <span class="chip chip--danger">失败</span>
        <span class="chip chip--info">信息</span>
      </div>
      <div class="theme-preview__row">
        <label><input type="checkbox" v-model="checked" /> 复选框</label>
        <label><input type="radio" name="r" value="a" v-model="radio" /> 选项 A</label>
        <label><input type="radio" name="r" value="b" v-model="radio" /> 选项 B</label>
        <label><input type="checkbox" role="switch" v-model="toggle" /> 开关</label>
      </div>
      <progress :value="0.62" max="1" aria-label="进度示例"></progress>
    </section>

    <!-- ③ 输入 -->
    <section class="theme-preview__section" aria-labelledby="p-inputs">
      <h2 id="p-inputs">③ 输入（Inputs）</h2>
      <div class="theme-preview__grid">
        <label>文本<input v-model="sampleText" placeholder="输入文本" /></label>
        <label>下拉
          <select v-model="sampleSelect">
            <option value="active">激活</option>
            <option value="pending">待审核</option>
            <option value="archived">已归档</option>
          </select>
        </label>
        <label>错误态<input value="必填" aria-invalid="true" /><small role="alert">错误说明始终可见</small></label>
        <label>只读<input value="锁定值" readonly /></label>
        <label>禁用<input value="禁用值" disabled /></label>
      </div>
    </section>

    <!-- ④ 列表 -->
    <section class="theme-preview__section" aria-labelledby="p-table">
      <h2 id="p-table">④ 列表（Lists）</h2>
      <label><input type="checkbox" v-model="tableEmpty" /> 空态</label>
      <label><input type="checkbox" v-model="tableLoading" /> 加载</label>
      <table>
        <thead><tr><th>名称</th><th>状态</th><th>更新时间</th><th>操作</th></tr></thead>
        <tbody v-if="tableLoading"><tr><td colspan="4">加载中…</td></tr></tbody>
        <tbody v-else-if="tableEmpty"><tr><td colspan="4">暂无数据</td></tr></tbody>
        <tbody v-else>
          <tr><td>设计规范</td><td><span class="chip chip--success">已发布</span></td><td>2026-11-08</td><td><button class="btn btn--text">详情</button></td></tr>
          <tr><td>审查报告</td><td><span class="chip chip--warning">待审核</span></td><td>2026-11-08</td><td><button class="btn btn--text">详情</button></td></tr>
          <tr><td>同步任务</td><td><span class="chip chip--danger">失败</span></td><td>2026-11-08</td><td><button class="btn btn--text">重试</button></td></tr>
        </tbody>
      </table>
    </section>

    <!-- ⑤ 分组 -->
    <section class="theme-preview__section" aria-labelledby="p-tabs">
      <h2 id="p-tabs">⑤ 分组（Groups）</h2>
      <nav role="tablist">
        <button role="tab" :aria-selected="tab==='a'" @click="tab='a'">概览</button>
        <button role="tab" :aria-selected="tab==='b'" @click="tab='b'">详情</button>
        <button role="tab" :aria-selected="tab==='c'" @click="tab='c'">历史</button>
      </nav>
      <div v-show="tab==='a'" role="tabpanel"><p>概览面板</p></div>
      <div v-show="tab==='b'" role="tabpanel"><p>详情面板</p></div>
      <div v-show="tab==='c'" role="tabpanel"><p>历史面板</p></div>
    </section>

    <!-- ⑥ 浮层 -->
    <section class="theme-preview__section" aria-labelledby="p-overlay">
      <h2 id="p-overlay">⑥ 浮层（Overlays）</h2>
      <button class="btn btn--primary" @click="dialogOpen = true">打开对话框</button>
      <div v-if="dialogOpen" role="dialog" aria-modal="true" class="dialog">
        <header><h3>确认操作</h3><button @click="dialogOpen = false" aria-label="关闭">×</button></header>
        <p>这是一段对话框说明。</p>
        <footer><button class="btn btn--text" @click="dialogOpen = false">取消</button><button class="btn btn--primary" @click="dialogOpen = false">确认</button></footer>
      </div>
    </section>

    <!-- ⑦ 反馈 -->
    <section class="theme-preview__section" aria-labelledby="p-feedback">
      <h2 id="p-feedback">⑦ 反馈（Feedback）</h2>
      <div class="banner banner--success" role="status">操作成功</div>
      <div class="banner banner--info" role="status">提示信息</div>
      <div class="banner banner--warning" role="alert">警告说明</div>
      <div class="banner banner--danger" role="alert">错误原因 + 重试路径</div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const checked = ref(true)
const radio = ref('a')
const toggle = ref(true)
const sampleText = ref('Aranea Workspace')
const sampleSelect = ref('active')
const tableEmpty = ref(false)
const tableLoading = ref(false)
const tab = ref<'a' | 'b' | 'c'>('a')
const dialogOpen = ref(false)

function toggleTheme() {
  const html = document.documentElement
  const cur = html.dataset.theme === 'light' ? 'dark' : 'light'
  html.dataset.theme = cur
}
</script>

<style scoped>
/* 全部走 --nt-* token，不出现裸色 */
.theme-preview { padding: 24px; max-width: 1280px; margin: 0 auto; color: var(--nt-text-1); background: var(--nt-bg-0); min-height: 100vh; }
.theme-preview__head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; }
.theme-preview__section { margin-bottom: 32px; padding: 16px; background: var(--nt-bg-1); border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-l); }
.theme-preview__section h2 { font-size: 15px; color: var(--nt-text-1); margin: 0 0 12px; }
.theme-preview__row { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; margin-bottom: 12px; }
.theme-preview__grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 12px; }
.theme-preview__grid label { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--nt-text-2); }
.theme-preview__grid input, .theme-preview__grid select { padding: 8px 12px; border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-m); background: var(--nt-bg-2); color: var(--nt-text-1); font-size: 13px; }
.theme-preview__grid input:focus, .theme-preview__grid select:focus { outline: 2px solid var(--nt-accent); outline-offset: 2px; }
.theme-preview__grid input[aria-invalid="true"] { border-color: var(--nt-danger); }
.theme-preview__grid small[role="alert"] { color: var(--nt-danger); }

.btn { padding: 8px 16px; border-radius: var(--nt-radius-m); font-size: 13px; font-weight: 600; cursor: pointer; border: 1px solid transparent; transition: background var(--nt-transition); }
.btn--primary { background: var(--nt-accent); color: var(--nt-on-accent); }
.btn--primary:hover:not(:disabled) { background: var(--nt-accent-hover); }
.btn--secondary { background: transparent; color: var(--nt-accent); border-color: var(--nt-accent-border); }
.btn--text { background: transparent; color: var(--nt-accent); }
.btn--danger { background: var(--nt-danger); color: var(--nt-on-accent); }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn:focus-visible { outline: 2px solid var(--nt-accent); outline-offset: 2px; }

.chip { padding: 4px 10px; border-radius: var(--nt-radius-pill); font-size: 11px; font-weight: 500; }
.chip--success { background: var(--nt-accent-soft); color: var(--nt-success); }
.chip--warning { background: var(--nt-accent-soft); color: var(--nt-warn); }
.chip--danger { background: var(--nt-accent-soft); color: var(--nt-danger); }
.chip--info { background: var(--nt-accent-soft); color: var(--nt-info); }

table { width: 100%; border-collapse: collapse; font-size: 13px; }
th { text-align: left; padding: 8px 12px; background: var(--nt-bg-2); color: var(--nt-text-2); font-weight: 500; }
td { padding: 12px; border-top: 1px solid var(--nt-border-1); }
tbody tr:hover { background: var(--nt-accent-soft); }

nav[role="tablist"] { display: flex; gap: 4px; border-bottom: 1px solid var(--nt-border-1); }
nav[role="tablist"] button { padding: 8px 16px; background: transparent; border: none; border-bottom: 2px solid transparent; color: var(--nt-text-2); cursor: pointer; }
nav[role="tablist"] button[aria-selected="true"] { color: var(--nt-accent); border-bottom-color: var(--nt-accent); }

.dialog { position: fixed; inset: 0; background: var(--nt-scrim); display: flex; align-items: center; justify-content: center; z-index: 100; }
.dialog > * { background: var(--nt-bg-2); border-radius: var(--nt-radius-l); padding: 24px; max-width: 480px; width: 90%; }

.banner { padding: 12px 16px; border-radius: var(--nt-radius-m); margin-bottom: 8px; font-size: 13px; }
.banner--success { background: var(--nt-accent-soft); color: var(--nt-success); }
.banner--info { background: var(--nt-accent-soft); color: var(--nt-info); }
.banner--warning { background: var(--nt-accent-soft); color: var(--nt-warn); }
.banner--danger { background: var(--nt-accent-soft); color: var(--nt-danger); }

progress { width: 100%; height: 6px; border-radius: var(--nt-radius-pill); }
</style>
