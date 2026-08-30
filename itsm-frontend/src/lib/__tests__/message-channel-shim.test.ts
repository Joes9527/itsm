describe('jest MessageChannel shim', () => {
  it('delivers a posted message before setup teardown closes registered channels', async () => {
    const channel = new MessageChannel();

    const received = new Promise((resolve, reject) => {
      const timeoutId = setTimeout(() => {
        reject(new Error('timed out waiting for MessageChannel delivery'));
      }, 1000);

      channel.port1.onmessage = (event) => {
        clearTimeout(timeoutId);
        resolve(event.data);
      };

      channel.port1.onmessageerror = (event) => {
        clearTimeout(timeoutId);
        reject(event);
      };
    });

    channel.port2.postMessage({ type: 'delivery-proof' });

    await expect(received).resolves.toEqual({ type: 'delivery-proof' });
  });
});
