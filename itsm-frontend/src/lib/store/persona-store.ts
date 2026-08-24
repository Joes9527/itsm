import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { PersonaType, PERSONAS, getRolePersonaConfig } from '@/config/persona/persona-config';

interface PersonaState {
  activePersona: PersonaType;
  // 记录上一次调用 initPersonaByRole 时的用户 ID——persist 用的 localStorage key
  // ('itsm-active-persona') 不按用户隔离，同一浏览器换账号登录时，如果新用户角色的
  // allowedPersonas 恰好也包含上一个用户选的 persona，仅凭"是否在允许列表里"判断
  // 不出重置，会把上一个用户的 persona 选择带到新用户账号下。
  lastUserId: number | null;
  setActivePersona: (persona: PersonaType) => void;
  initPersonaByRole: (roleCode?: string, userId?: number) => void;
}

export const usePersonaStore = create<PersonaState>()(
  persist(
    (set, get) => ({
      activePersona: 'portal',
      lastUserId: null,
      setActivePersona: (persona: PersonaType) => {
        set({ activePersona: persona });
      },
      initPersonaByRole: (roleCode?: string, userId?: number) => {
        const config = getRolePersonaConfig(roleCode);
        const { activePersona: current, lastUserId } = get();
        const identity = userId ?? null;
        // 换了一个不同的用户（或第一次拿到明确身份）：不管旧值是否恰好也在新角色的
        // allowedPersonas 里，一律重置为新角色的默认 persona，避免残留上一个用户的选择。
        if (identity !== null && identity !== lastUserId) {
          set({ activePersona: config.defaultPersona, lastUserId: identity });
          return;
        }
        // 同一个用户：仅当当前保存的 persona 不在允许列表内时才重置，
        // 保留用户在本次登录会话里手动切换过的 persona。
        if (!config.allowedPersonas.includes(current)) {
          set({ activePersona: config.defaultPersona });
        }
      },
    }),
    {
      name: 'itsm-active-persona',
    }
  )
);
