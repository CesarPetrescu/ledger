import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import reactHooks from 'eslint-plugin-react-hooks'

export default tseslint.config(
  { ignores: ['dist', 'node_modules'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  reactHooks.configs.flat.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    rules: {
      // Memory content is untrusted; never hand it to the HTML parser.
      'no-restricted-syntax': [
        'error',
        {
          selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
          message: 'Render untrusted content as text, never as HTML.',
        },
        {
          selector: "JSXAttribute[name.name='style']",
          message: 'Inline styles are blocked by the CSP; use classes and data attributes.',
        },
        {
          selector: "MemberExpression[property.name='innerHTML']",
          message: 'Do not write HTML strings into the DOM.',
        },
      ],
    },
  },
)
