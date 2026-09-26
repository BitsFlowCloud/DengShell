/* Public server addresses are metadata; the one-second traffic feed is separate. */
'use strict';
function renderServerAddresses(stats) {
  const sources = new Map((stats?.serverAddressInfo || []).map(value => [value.address, value]));
  const describe = address => {
    const info = sources.get(address);
    if (info?.source === 'ssh-entry') return `${address} · SSH 连接入口（可能经过 NAT，不代表服务器出口）`;
    if (info?.source === 'ssh-server') return `${address} · SSH 服务端地址（远端连接确认）`;
    if (info?.source === 'interface') return `${address} · 服务器网卡${info.interface ? ' ' + info.interface : ''}`;
    return `${address} · 服务器公网地址`;
  };
  for (const [id, addresses, family] of [['server-ipv4', stats?.serverIPv4, 'IPv4'], ['server-ipv6', stats?.serverIPv6, 'IPv6']]) {
    const element = $(`#${id}`), list = [...(addresses || [])];
    const detail = list.length ? list.map(describe).join('\n') : `没有可确认的公网 ${family} 地址；内网、环回和链路本地地址仅显示在网卡信息中`;
    const cadence = '\n网卡地址约每 30 秒更新；未请求外部查 IP 服务';
    element.textContent = list[0] || '-'; element.title = detail + cadence;
    const button = element.closest('.address-copy'); button.disabled = !list.length;
    button.title = (list.length ? `点击复制 ${list[0]}\n${detail}` : detail) + cadence;
  }
}
