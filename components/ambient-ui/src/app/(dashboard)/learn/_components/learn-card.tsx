import Link from 'next/link'
import { Card, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'

type LearnCardProps = {
  title: string
  description: string
  href: string
}

export function LearnCard({ title, description, href }: LearnCardProps) {
  const safeHref = href.startsWith('/learn/') ? href : '/learn'
  return (
    <Link href={safeHref} className="group">
      <Card className="h-full transition-colors group-hover:border-primary/50">
        <CardHeader>
          <CardTitle className="text-base">{title}</CardTitle>
          {description && (
            <CardDescription className="line-clamp-3">{description}</CardDescription>
          )}
        </CardHeader>
      </Card>
    </Link>
  )
}
