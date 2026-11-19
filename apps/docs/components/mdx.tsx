import defaultMdxComponents from 'fumadocs-ui/mdx';
import type { MDXComponents } from 'mdx/types';

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    img: (props) => {
      const src = typeof props.src === 'string' ? props.src : undefined;
      const name = src?.match(/(?:^|\/)(direct-mirror|oci-mirror-proxy)(?:[./])/)?.[1];
      if (name && src?.endsWith('.gif')) return <defaultMdxComponents.img {...props} src={`/demos/${name}.webp`} width={1104} height={726} loading="lazy" decoding="async" fetchPriority="low" />;
      return <defaultMdxComponents.img {...props} />;
    },
    ...components,
  } satisfies MDXComponents;
}

export const useMDXComponents = getMDXComponents;

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>;
}
