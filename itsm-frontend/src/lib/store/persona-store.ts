import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { PersonaType, PERSONAS, getRolePersonaConfig } from '@/config/persona/persona-config';

interface PersonaState {
  activePersona: PersonaType;
  setActivePersona: (persona: PersonaType) => void;
  initPersonaByRole: (roleCode?: string) => void;
}

export const usePersonaStore = create<PersonaState>()(
  persist(
    (set, get) => ({
      activePersona: 'portal',
      setActivePersona: (persona: PersonaType) => {
        set({ activePersona: persona });
      },
      initPersonaByRole: (roleCode?: string) => {
        const config = getRolePersonaConfig(roleCode);
        const current = get().activePersona;
        // 如果当前保存的 persona 不在允许列表内，则重置为默认
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
