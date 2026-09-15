import React, { useState, useEffect } from 'react';
import { Box, Text, Spacer } from 'ink';
import { apiClient } from '../lib/api-client.js';

interface LoginProps {
  username: string;
  password: string;
}

export const LoginCommand: React.FC<LoginProps> = ({ username, password }) => {
  const [done, setDone] = useState<'ok' | 'error' | null>(null);
  const [message, setMessage] = useState('');

  useEffect(() => {
    if (!username || !password) {
      setMessage('Usage: itsm login -u <username> -p <password>');
      setDone('error');
      return;
    }
    apiClient.login({ username, password })
      .then(result => {
        setMessage(`Logged in as ${result.user.name} (${result.tenant.name})`);
        setDone('ok');
      })
      .catch(err => {
        setMessage(err instanceof Error ? err.message : 'Login failed');
        setDone('error');
      });
  }, []);

  if (done === null) {
    return <Text color="cyan">Logging in...</Text>;
  }

  return (
    <Box flexDirection="column">
      <Text color={done === 'ok' ? 'green' : 'red'}>{done === 'ok' ? '✓' : '✗'} {message}</Text>
      {done === 'ok' && <Text dimColor>Secure session saved to ~/.itsm/credentials</Text>}
    </Box>
  );
};

export const LogoutCommand: React.FC = () => {
  const [state, setState] = useState<'loading' | 'ok' | 'error'>('loading');

  useEffect(() => {
    apiClient.logout()
      .then(() => setState('ok'))
      .catch(() => setState('error'));
  }, []);

  if (state === 'loading') return <Text color="cyan">Logging out...</Text>;
  if (state === 'error') return <Text color="red">✗ Logout failed; local session cleared</Text>;
  return <Text color="yellow">✓ Logged out</Text>;
};
