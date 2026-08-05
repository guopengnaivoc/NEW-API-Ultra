/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { ReactNode } from 'react'
import type { FootnoteNode, ParsedNode } from 'stream-markdown-parser'

import { getNodeKey } from './response-content'
import {
  hasParsedChildren,
  isBlockquoteNode,
  isCodeBlockNode,
  isDefinitionListNode,
  isHeadingNode,
  isHtmlBlockNode,
  isImageNode,
  isLinkNode,
  isListNode,
  isMathBlockNode,
  isMathInlineNode,
  isTableNode,
  isTextNode,
} from './response-node-guards'
import { renderBlockquote } from './response-renderer-alert'
import {
  renderCodeBlock,
  renderDefinitionList,
  renderHeading,
  renderList,
  renderMathBlock,
  renderMathInline,
} from './response-renderer-blocks'
import { renderDetails } from './response-renderer-details'
import {
  getFootnoteDefinitionId,
  getFootnoteReferenceId,
  renderFootnotes as renderFootnotesBlock,
} from './response-renderer-footnotes'
import {
  renderImage,
  renderLink,
  renderTextNode,
} from './response-renderer-inline'
import { renderTable } from './response-renderer-table'
import type { RenderChildren } from './response-types'

type ResponseRendererContext = {
  footnoteDefinitionIndices: Map<string, number>
  footnoteFirstReferenceIds: Map<string, string>
  footnoteReferenceIds: WeakMap<ParsedNode, string>
  renderChildren: RenderChildren
  responseId: string
}

export function createResponseRenderer(
  responseId: string,
  bodyNodes: ParsedNode[],
  footnotes: FootnoteNode[]
) {
  const footnoteFirstReferenceIds = new Map<string, string>()
  const footnoteReferenceIds = new WeakMap<ParsedNode, string>()
  const pendingNodes = [...bodyNodes, ...footnotes].reverse()
  const visitedNodes = new WeakSet<ParsedNode>()
  let nextFootnoteReferenceIndex = 1

  while (pendingNodes.length > 0) {
    const node = pendingNodes.pop()
    if (!node || visitedNodes.has(node)) {
      continue
    }
    visitedNodes.add(node)

    if (node.type === 'footnote_reference' && 'id' in node) {
      const footnoteId = String(node.id)
      const referenceId = getFootnoteReferenceId(
        responseId,
        nextFootnoteReferenceIndex
      )
      nextFootnoteReferenceIndex += 1
      footnoteReferenceIds.set(node, referenceId)
      if (!footnoteFirstReferenceIds.has(footnoteId)) {
        footnoteFirstReferenceIds.set(footnoteId, referenceId)
      }
      continue
    }

    let renderedChildren: ParsedNode[] = []
    if (isListNode(node)) {
      renderedChildren = node.items.flatMap((item) => item.children)
    } else if (isTableNode(node)) {
      renderedChildren = [
        ...node.header.cells.flatMap((cell) => cell.children),
        ...node.rows.flatMap((row) =>
          row.cells.flatMap((cell) => cell.children)
        ),
      ]
    } else if (isDefinitionListNode(node)) {
      renderedChildren = node.items.flatMap((item) => [
        ...item.term,
        ...item.definition,
      ])
    } else if (
      isHtmlBlockNode(node) &&
      node.tag === 'details' &&
      hasParsedChildren(node)
    ) {
      const summaryNode = node.children.find(
        (child) => isHtmlBlockNode(child) && child.tag === 'summary'
      )
      const summaryChildren =
        summaryNode && hasParsedChildren(summaryNode)
          ? summaryNode.children
          : []
      renderedChildren = [
        ...summaryChildren,
        ...node.children.filter((child) => child !== summaryNode),
      ]
    } else if (
      hasParsedChildren(node) &&
      node.type !== 'html_block' &&
      node.type !== 'html_inline'
    ) {
      renderedChildren = node.children
    }

    for (let index = renderedChildren.length - 1; index >= 0; index -= 1) {
      pendingNodes.push(renderedChildren[index])
    }
  }

  const context: ResponseRendererContext = {
    footnoteDefinitionIndices: new Map(
      footnotes.map((footnote, index) => [String(footnote.id), index + 1])
    ),
    footnoteFirstReferenceIds,
    footnoteReferenceIds,
    renderChildren: (nodes) =>
      nodes.map((node, index) =>
        renderNode(node, getNodeKey(node, index), context)
      ),
    responseId,
  }

  return {
    renderChildren: context.renderChildren,
    renderFootnotes: (footnotes: FootnoteNode[]) =>
      renderFootnotesBlock(
        footnotes,
        { renderChildren: context.renderChildren },
        responseId,
        context.footnoteFirstReferenceIds
      ),
  }
}

function renderNode(
  node: ParsedNode,
  key: string,
  context: ResponseRendererContext
): ReactNode {
  const { renderChildren } = context

  if (isTextNode(node)) {
    return renderTextNode(node)
  }

  if (isHeadingNode(node)) {
    return renderHeading(node, key, { renderChildren })
  }

  if (node.type === 'paragraph' && hasParsedChildren(node)) {
    return (
      <p className='my-3 leading-7' key={key}>
        {renderChildren(node.children)}
      </p>
    )
  }

  if (node.type === 'inline' && hasParsedChildren(node)) {
    return <span key={key}>{renderChildren(node.children)}</span>
  }

  if (isListNode(node)) {
    return renderList(node, key, { renderChildren })
  }

  if (isCodeBlockNode(node)) {
    return renderCodeBlock(node, key)
  }

  if (node.type === 'inline_code' && 'code' in node) {
    return (
      <code
        className='bg-muted/70 text-foreground rounded px-1 py-0.5 font-mono text-[0.9em]'
        key={key}
      >
        {String(node.code)}
      </code>
    )
  }

  if (isLinkNode(node)) {
    return renderLink(node, key, renderChildren)
  }

  if (isImageNode(node)) {
    return renderImage(node, key)
  }

  if (isBlockquoteNode(node)) {
    return renderBlockquote(node, key, { renderChildren })
  }

  if (isTableNode(node)) {
    return renderTable(node, key, { renderChildren })
  }

  if (isDefinitionListNode(node)) {
    return renderDefinitionList(node, key, { renderChildren })
  }

  if (node.type === 'strong' && hasParsedChildren(node)) {
    return (
      <strong className='text-foreground font-semibold' key={key}>
        {renderChildren(node.children)}
      </strong>
    )
  }

  if (node.type === 'emphasis' && hasParsedChildren(node)) {
    return <em key={key}>{renderChildren(node.children)}</em>
  }

  if (node.type === 'strikethrough' && hasParsedChildren(node)) {
    return <del key={key}>{renderChildren(node.children)}</del>
  }

  if (node.type === 'highlight' && hasParsedChildren(node)) {
    return <mark key={key}>{renderChildren(node.children)}</mark>
  }

  if (node.type === 'insert' && hasParsedChildren(node)) {
    return <ins key={key}>{renderChildren(node.children)}</ins>
  }

  if (node.type === 'subscript' && hasParsedChildren(node)) {
    return <sub key={key}>{renderChildren(node.children)}</sub>
  }

  if (node.type === 'superscript' && hasParsedChildren(node)) {
    return <sup key={key}>{renderChildren(node.children)}</sup>
  }

  if (
    (node.type === 'checkbox' || node.type === 'checkbox_input') &&
    'checked' in node
  ) {
    return (
      <input
        checked={Boolean(node.checked)}
        className='accent-primary mr-2 size-4 align-[-0.15em]'
        disabled
        key={key}
        readOnly
        type='checkbox'
      />
    )
  }

  if (node.type === 'hardbreak') {
    return <br key={key} />
  }

  if (node.type === 'thematic_break') {
    return <hr className='border-border/70 my-6' key={key} />
  }

  if (isMathBlockNode(node)) {
    return renderMathBlock(node, key)
  }

  if (isMathInlineNode(node)) {
    return renderMathInline(node, key)
  }

  if (node.type === 'footnote_reference' && 'id' in node) {
    const footnoteId = String(node.id)
    const definitionIndex = context.footnoteDefinitionIndices.get(footnoteId)
    const referenceId = context.footnoteReferenceIds.get(node)

    if (definitionIndex === undefined || referenceId === undefined) {
      return (
        <sup className='text-primary mx-0.5 text-xs' key={key}>
          [{footnoteId}]
        </sup>
      )
    }

    return (
      <sup className='text-primary mx-0.5 text-xs' key={key}>
        <a
          className='underline-offset-2 hover:underline'
          href={`#${getFootnoteDefinitionId(context.responseId, definitionIndex)}`}
          id={referenceId}
        >
          [{footnoteId}]
        </a>
      </sup>
    )
  }

  if (node.type === 'footnote_anchor') {
    return null
  }

  if (isHtmlBlockNode(node) && node.tag === 'details') {
    return renderDetails(node, key, { renderChildren })
  }

  if (node.type === 'html_block' && 'content' in node) {
    return <span key={key}>{String(node.content)}</span>
  }

  if (node.type === 'html_inline' && 'content' in node) {
    return <span key={key}>{String(node.content)}</span>
  }

  if (hasParsedChildren(node)) {
    return <span key={key}>{renderChildren(node.children)}</span>
  }

  if ('content' in node && typeof node.content === 'string') {
    return <span key={key}>{node.content}</span>
  }

  return <span key={key}>{node.raw}</span>
}
