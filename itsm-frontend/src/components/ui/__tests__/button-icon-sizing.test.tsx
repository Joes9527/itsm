/**
 * 按钮图标尺寸机制的回归测试。
 *
 * 这轮 lucide -> @ant-design/icons 迁移的**全部理由**是这一条事实，所以它值得一条测试
 * 钉住，而不是靠人记得：
 *
 *   antd 的 resetIcon() 只给 .ant-btn-icon > svg 设 display/color/line-height/
 *   vertical-align，**从不设 width/height/font-size**，尺寸完全靠继承按钮字号；
 *   而 lucide-react 把 width/height 写成 SVG **呈现属性**，呈现属性的优先级高于继承，
 *   所以只要按钮里还用 lucide，它那个 size={N} / 默认 24 就永远赢。
 *
 * 换句话说：下面第二条测试里的 '24' 就是当初 62 处「24px 图标塞进 29px 小按钮」的根因。
 * 如果哪天有人把按钮图标换回 lucide，或者升级 lucide 后它改了默认值，这条会先响。
 */

import React from 'react';
import { render } from '@testing-library/react';
import { Button } from 'antd';
import { DeleteOutlined } from '@ant-design/icons';
import { Trash2 } from 'lucide-react';

const iconSvg = (container: HTMLElement): SVGElement => {
  const svg = container.querySelector('.ant-btn-icon svg');
  if (!svg) throw new Error('按钮里没有 .ant-btn-icon svg —— antd 的图标容器结构变了？');
  return svg as unknown as SVGElement;
};

describe('按钮图标尺寸：antd 继承 vs lucide 写死', () => {
  it('antd 图标用 1em，尺寸交给按钮字号继承', () => {
    const { container } = render(<Button icon={<DeleteOutlined aria-hidden="true" />}>删除</Button>);
    const svg = iconSvg(container);
    expect(svg.getAttribute('width')).toBe('1em');
    expect(svg.getAttribute('height')).toBe('1em');
  });

  it('lucide 图标把 width/height 写成呈现属性，与按钮字号无关 —— 这就是必须迁移的原因', () => {
    const { container } = render(<Button icon={<Trash2 />}>删除</Button>);
    const svg = iconSvg(container);
    // lucide 的默认 size 是 24，会盖过 antd 想表达的「继承按钮字号」
    expect(svg.getAttribute('width')).toBe('24');
    expect(svg.getAttribute('height')).toBe('24');
  });

  it('lucide 的 size 也是这样写死的，不会随按钮 size 变化', () => {
    const { container } = render(
      <Button size="small" icon={<Trash2 size={13} />}>
        删除
      </Button>
    );
    const svg = iconSvg(container);
    expect(svg.getAttribute('width')).toBe('13');
  });
});
