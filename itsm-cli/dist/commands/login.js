import React, { useState, useEffect } from 'react';
import { Box, Text } from 'ink';
import { apiClient } from '../lib/api-client.js';
export const LoginCommand = ({ username, password }) => {
    const [done, setDone] = useState(null);
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
        return React.createElement(Text, { color: "cyan" }, "Logging in...");
    }
    return (React.createElement(Box, { flexDirection: "column" },
        React.createElement(Text, { color: done === 'ok' ? 'green' : 'red' },
            done === 'ok' ? '✓' : '✗',
            " ",
            message),
        done === 'ok' && React.createElement(Text, { dimColor: true }, "Secure session saved to ~/.itsm/credentials")));
};
export const LogoutCommand = () => {
    const [state, setState] = useState('loading');
    useEffect(() => {
        apiClient.logout()
            .then(() => setState('ok'))
            .catch(() => setState('error'));
    }, []);
    if (state === 'loading')
        return React.createElement(Text, { color: "cyan" }, "Logging out...");
    if (state === 'error')
        return React.createElement(Text, { color: "red" }, "\u2717 Logout failed; local session cleared");
    return React.createElement(Text, { color: "yellow" }, "\u2713 Logged out");
};
