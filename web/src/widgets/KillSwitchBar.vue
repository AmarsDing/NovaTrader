<script setup lang="ts">
/**
 * Kill Switch 常驻条（原 component/KillSwitchButton.vue）。
 * 触发/恢复二合一弹层，恢复必须输入 CONFIRM；查看角色锁定。
 * 玻璃+发光只在此弹层与按钮 hot 态（发光预算允许）。
 */
import { ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import { messageOf } from '@/utils/format'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtModal from '@/ui/NtModal.vue'

const auth = useAuthStore()
const risk = useRiskStore()

const open = ref(false)
const reason = ref('手动急停')
const confirmWord = ref('')
const error = ref('')
const busy = ref(false)

async function submit() {
  if (auth.isViewer() || busy.value) return
  error.value = ''
  if (risk.killActive && confirmWord.value !== 'CONFIRM') {
    error.value = '恢复必须输入 CONFIRM'
    return
  }
  busy.value = true
  try {
    const operator = auth.operatorName
    if (risk.killActive) {
      await risk.reset({ operator, confirm: 'CONFIRM', reason: reason.value || '客户端恢复' })
    } else {
      await risk.trigger({ source: 'client', reason: reason.value || '手动急停', operator })
    }
    open.value = false
    confirmWord.value = ''
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <NtButton
    :variant="risk.killActive ? 'danger' : 'ghost'"
    :class="{ 'kill--hot': risk.killActive }"
    @click="open = true"
  >
    {{ risk.killActive ? 'Kill Switch 已触发' : 'Kill Switch' }}
  </NtButton>

  <NtModal v-model:open="open" :title="risk.killActive ? '恢复 Kill Switch' : '触发 Kill Switch'" width="440px">
    <p class="nt-note">
      {{
        risk.killActive
          ? `来源 ${risk.killSource || '—'}。恢复后模式仍是 L0。`
          : '停止新开仓，并请求撤销未成交委托。模式降到 L0。'
      }}
    </p>
    <NtInput v-model="reason" label="原因" />
    <NtInput
      v-if="risk.killActive"
      v-model="confirmWord"
      label="确认字样"
      placeholder="CONFIRM"
      mono
    />
    <p v-if="auth.isViewer()" class="nt-err">当前是查看角色，不能操作 Kill Switch。</p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <template #footer>
      <NtButton @click="open = false">关闭</NtButton>
      <NtButton
        :variant="risk.killActive ? 'primary' : 'danger'"
        :disabled="auth.isViewer() || busy"
        :loading="busy"
        @click="submit"
      >
        {{ risk.killActive ? '恢复' : '触发' }}
      </NtButton>
    </template>
  </NtModal>
</template>

<style scoped>
/* Kill Switch hot 态发光：发光预算允许的一处。 */
.kill--hot {
  box-shadow: 0 0 18px rgba(255, 58, 58, 0.45);
}

.nt-note {
  margin: 0;
}
</style>
