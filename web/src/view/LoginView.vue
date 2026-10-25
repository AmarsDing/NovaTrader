<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import NovaStatusPulse from '@/component/NovaStatusPulse.vue'
import BlankLayout from '@/layout/BlankLayout.vue'
import { healthz } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import { getHost, setHost } from '@/utils/endpoint'
import { messageOf } from '@/utils/format'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const host = ref(getHost())
const health = ref('检测中')

/** 连接三态按结局着色（直觉5/2）：检测中=中性，可达=success，无响应=danger——不再同色装傻。 */
const healthTone = computed(() =>
  health.value === '检测中' ? 'checking' : health.value === '2001 可达' ? 'ok' : 'bad',
)

function goNext() {
  const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
  router.replace(redirect || '/')
}

async function check() {
  try {
    host.value = setHost(host.value)
  } catch (err) {
    health.value = messageOf(err)
    return
  }
  health.value = (await healthz()) ? '2001 可达' : '2001 无响应'
}

async function onSubmit() {
  try {
    setHost(host.value)
  } catch (err) {
    health.value = messageOf(err)
    return
  }
  const ok = await auth.login(username.value, password.value)
  if (ok) goNext()
}

async function onLocal() {
  const ok = await auth.enterLocal(username.value)
  if (ok) goNext()
}

onMounted(check)
</script>

<template>
  <BlankLayout>
    <div class="card">
      <div class="head">
        <NovaStatusPulse />
        <div>
          <h1 class="title">NovaTrader 智脑</h1>
          <p class="desc">AI 驱动的超短线交易控制中枢</p>
        </div>
      </div>

      <form class="form" @submit.prevent="onSubmit">
        <NtInput v-model="host" label="admin 主机" mono @blur="check" />
        <p class="status" :class="`status--${healthTone}`">连接 {{ health }} · HTTP 2001 · WebSocket 2003</p>
        <NtInput v-model="username" label="账号" placeholder="用户名 / 邮箱" />
        <NtInput v-model="password" label="密码" type="password" placeholder="••••••••" />

        <p v-if="auth.error" class="err">{{ auth.error }}<span v-if="healthTone === 'bad'">——先启动 admin 服务再登录</span></p>

        <NtButton type="submit" variant="primary" block :loading="auth.loading">
          {{ auth.loading ? '验证中…' : '进入驾驶舱' }}
        </NtButton>
        <NtButton v-if="auth.canLocal" block @click="onLocal">仍进入驾驶舱（本地会话）</NtButton>
      </form>

      <p class="hint">桌面端把令牌写入系统凭据库。本地会话不会伪造账户数字，顶栏会标明不是实时。</p>
    </div>
  </BlankLayout>
</template>

<style scoped>
.card {
  width: min(420px, 100%);
  padding: 28px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-l);
}

.head {
  display: flex;
  gap: 14px;
  align-items: flex-start;
  margin-bottom: 24px;
}

.title {
  margin: 0;
  font-size: var(--nt-fs-hero); /* 收编自 20px 裸值（§5.3 hero 档 24 单面板≤1 组——登录页唯一焦点恰是它） */
  font-weight: 700;
  color: var(--nt-text-1);
}

.desc {
  margin: 4px 0 0;
  font-size: var(--nt-fs-panel); /* 收编自 13px（panel 档，同值） */
  color: var(--nt-text-2);
}

.form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.status {
  margin: -8px 0 0;
  font-size: var(--nt-fs-caption); /* 收编自 11.5px 裸值（caption 档，同值） */
  color: var(--nt-text-2);
}

.status--ok {
  color: var(--nt-success);
}

.status--bad {
  color: var(--nt-danger);
}

.err {
  margin: 0;
  font-size: var(--nt-fs-body); /* 收编自 12.5px（body 档，同值） */
  color: var(--nt-danger);
}

.hint {
  margin: 18px 0 0;
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--nt-text-3);
}
</style>
